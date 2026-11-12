// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldbehavior

import (
	"errors"
	"fmt"
	"slices"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/fieldinfo"
	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskmerge"
)

// Strip walks msg and handles every populated field whose
// google.api.field_behavior annotation intersects the configured behavior set
// (see [WithBehaviors]). With no [WithBehaviors] passed, Strip is a no-op.
// The behavior of a field that carries several annotations is chosen by a
// fixed priority, whatever the declaration order: OUTPUT_ONLY, IDENTIFIER,
// IMMUTABLE, REQUIRED, then the order of the set.
//
// What happens to a matched field depends on its behavior (AIP-203):
//   - OUTPUT_ONLY is cleared and never reported, also under [WithStrict]:
//     a service must clear an output-only value from a request and must not
//     return an error for it.
//   - IMMUTABLE is compared with the stored value [WithCurrent] supplies: an
//     equal value is retained for subsequent planning, a different one is a
//     violation (INVALID_ARGUMENT). Without [WithCurrent] the stored value is
//     unknown, so the value is kept for the caller to compare.
//   - Every other behavior (IDENTIFIER, INPUT_ONLY, ...) is cleared, or
//     reported instead under [WithStrict].
//
// With [WithCurrent], msg is an update of current: the implied selection is
// merged into a private copy of current, and every changed IDENTIFIER or
// IMMUTABLE value is a violation, whether or not it is in the configured set.
// This includes protected siblings cleared by a oneof switch and protected
// descendants removed by a collection replacement. Validated values are kept
// so a later planner can compare them even inside an atomic replacement.
//
// Strip traverses singular sub-messages, repeated message elements, and map
// values. Cleared or rejected fields are not descended. Retained protected
// values are descended to clear OUTPUT_ONLY input, preserving their other
// protected descendants. Collection elements are paired by list index or map key
// when current state is supplied. Equal IMMUTABLE element values are retained;
// other configured behaviors, including OUTPUT_ONLY, are handled as above.
//
// Under [WithFieldFormats] (on by default) the same walk also reports every
// string value it keeps that does not match the AIP-202 format of its field
// (google.api.field_info), with Behavior FIELD_BEHAVIOR_UNSPECIFIED; a field
// Strip clears or rejects is not checked.
//
// Strip checks and clears a private copy before publishing changes. Every
// error leaves msg untouched. Behavior violations match [ErrBehaviorViolation];
// a representation conversion failure matches [ErrCurrentMismatch] and retains
// its underlying cause. It returns [ErrMaxDepthExceeded] when [WithMaxDepth]
// is exceeded.
//
// Message types whose descriptor tree carries no relevant annotation are
// skipped without walking values, so [ErrMaxDepthExceeded] is only reported
// for subtrees that could actually match.
func Strip(msg proto.Message, opts ...Option) error {
	if nilcheck.IsNil(msg) {
		return nil
	}

	o := newOptions(opts...)
	if nilcheck.IsNil(o.resolver) {
		o.resolver = AnnotationResolver{}
	}

	if len(o.behaviors) == 0 {
		return nil
	}

	s := stripper{opts: o, relevant: o.behaviors, formats: o.formats(), resolve: o.resolveFunc()}
	_, s.standard = o.resolver.(AnnotationResolver)

	var current protoreflect.Message
	if !nilcheck.IsNil(o.current) {
		current = o.current.ProtoReflect()
		if !current.IsValid() || current.Descriptor() != msg.ProtoReflect().Descriptor() {
			return ErrCurrentMismatch
		}

		s.verify = true
		s.relevant = slices.Concat(o.behaviors, []annotations.FieldBehavior{annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_IMMUTABLE})
	}

	prf := msg.ProtoReflect()

	// Fast path: if no field reachable from this message type carries a
	// relevant behavior, the walk cannot clear or record anything. This is
	// the common case for unannotated responses.
	if s.standard && !s.subtreeRelevant(prf.Descriptor()) {
		return nil
	}

	s.segs = make(pathStack, 0, pathStackCapacity)

	var violations []BehaviorViolation
	if err := s.walk(prf, 0, &violations); err != nil {
		return err
	}

	if s.verify {
		var exceeded bool

		visit := func(_ string, depth int) bool {
			if depth > o.maxDepth {
				exceeded = true
				return false
			}

			return !exceeded
		}
		selection := maskmerge.ImpliedSelection(prf, s.resolve, maskmerge.DefaultAtomicMessage, o.maxDepth)

		changes, err := maskmerge.ProtectedChanges(prf, current, selection, s.resolve, "", visit)
		if err != nil {
			if errors.Is(err, maskmerge.ErrElementCountMismatch) {
				return err
			}

			return fmt.Errorf("%w: converting message: %w", ErrCurrentMismatch, err)
		}

		violations = append(violations, changes...)

		if exceeded {
			return ErrMaxDepthExceeded
		}
	}

	if len(violations) > 0 {
		return &BehaviorViolationError{Violations: violations}
	}

	// The check pass found nothing to clear: the apply pass would be a no-op.
	if s.clears == 0 {
		return nil
	}

	work := prf.New()
	if err := maskmerge.ReplaceMessage(work, prf); err != nil {
		return fmt.Errorf("%w: converting message: %w", ErrCurrentMismatch, err)
	}

	if err := s.walk(work, 0, nil); err != nil {
		return err
	}

	if err := maskmerge.ReplaceMessage(prf, work); err != nil {
		return fmt.Errorf("%w: converting message: %w", ErrCurrentMismatch, err)
	}

	return nil
}

// StripCreate clears fields marked OUTPUT_ONLY or IDENTIFIER. Use it on the
// resource embedded in a Create request before validation: AIP-203 accepts
// neither as input on create, and the server assigns the identifier. The
// default set is applied first; a caller's [WithBehaviors] overrides it
// entirely. Under [WithStrict] a populated IDENTIFIER is reported instead of
// cleared; OUTPUT_ONLY is always cleared silently. Follow it with
// [ValidateRequired] to reject absent or empty REQUIRED fields.
func StripCreate(msg proto.Message, opts ...Option) error {
	return Strip(msg, slices.Concat([]Option{WithBehaviors(DefaultCreateBehaviors()...)}, opts)...)
}

// StripUpdate sanitizes an Update resource whose populated fields are the
// update. OUTPUT_ONLY values are cleared. With [WithCurrent], a populated
// IMMUTABLE or IDENTIFIER value is compared by the same merge-effect evaluator
// as fieldmask/update. Equal protected values are retained for subsequent
// planning; a requested change, including a oneof sibling clear, is a violation.
// IDENTIFIER values remain available for routing. Collection elements are compared
// by list index or map key; equal immutable element values remain in the message.
//
// Without [WithCurrent], IMMUTABLE and IDENTIFIER values are retained and the
// caller must compare them with stored state. Use [WithCurrent], or the masked
// update APIs [github.com/altessa-s/go-aip/fieldmask/update.Spec.Plan] with
// WithCurrent and [github.com/altessa-s/go-aip/fieldmask/update.Patch]. StripUpdate
// has no mask: AIP-134's omitted mask implies populated fields. An unpopulated
// field is outside the selection, but a selected oneof sibling or whole collection
// can still clear it. Those APIs compare retained
// populated protected values using the implied mask. Protected-message equality
// ignores OUTPUT_ONLY descendants under the shared resolver; they remain cleared
// in the request and retain their stored values when protected fields are merged.
// Every validated protected value is retained, including descendants of mutable
// messages that a later planner may treat as atomic replacements.
// For masked two-stage updates, use [github.com/altessa-s/go-aip/fieldmask/update.Prepare] to retain clear intent.
// Do not replace the stored resource with a stripped request.
func StripUpdate(msg proto.Message, opts ...Option) error {
	return Strip(msg, slices.Concat([]Option{WithBehaviors(DefaultUpdateBehaviors()...)}, opts)...)
}

// StripResponse clears fields marked INPUT_ONLY at every depth. Use it on a
// server response before returning it to the caller so secrets (passwords,
// tokens) never leak through the read path (AIP-203: an input-only field is
// not included in output). A response is not input: field formats are not
// checked unless a caller's [WithFieldFormats](true) asks for it.
func StripResponse(msg proto.Message, opts ...Option) error {
	return Strip(msg, slices.Concat([]Option{WithBehaviors(DefaultResponseBehaviors()...), WithFieldFormats(false)}, opts)...)
}

// pathStackCapacity is the path depth a walk holds without growing its stack.
const pathStackCapacity = 8

type stripper struct {
	opts *options
	// relevant lists the behaviors the walk acts on: the configured set,
	// plus IDENTIFIER and IMMUTABLE when verifying against a current value.
	relevant []annotations.FieldBehavior
	// verify reports that a current value was supplied.
	verify bool
	// formats checks the field formats of kept values; nil when off.
	formats *fieldinfo.Checker
	// resolve reads the annotations of a field; the slice is read-only.
	resolve behavior.Resolve
	// standard reports the default annotation resolver, whose metadata the
	// descriptor-level subtree checks reflect.
	standard bool
	// clears counts the fields the check pass found to clear.
	clears int
	// segs is the path of the field being visited, rendered only when a
	// violation needs it.
	segs pathStack
}

// subtreeRelevant reports whether a field reachable from md carries a
// relevant behavior or, when formats are checked, a format.
func (s *stripper) subtreeRelevant(md protoreflect.MessageDescriptor) bool {
	return behavior.SubtreeHasAny(md, s.relevant...) || (s.formats != nil && s.formats.HasFormats(md))
}

// walk sanitizes populated fields. With violations non-nil it checks formats
// and records clears; with violations nil it applies them. Protected-update
// decisions are made separately by the shared merge evaluator before mutation.
func (s *stripper) walk(
	prf protoreflect.Message,
	depth int,
	violations *[]BehaviorViolation,
) error {
	if depth > s.opts.maxDepth {
		return ErrMaxDepthExceeded
	}

	md := prf.Descriptor()
	// Formats are checked only in the check pass and only in messages whose
	// schema holds a formatted field.
	checkFormats := violations != nil && s.formats != nil && s.formats.HasFormats(md)

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if !prf.Has(fd) {
			continue
		}

		s.segs.pushField(fd)
		err := s.visit(prf, fd, depth, checkFormats, violations)
		s.segs.pop()

		if err != nil {
			return err
		}
	}

	return nil
}

// action is what [stripper.visit] does with a populated field.
type action int

const (
	actionDescend action = iota // keep the field and walk its value
	actionClear                 // clear the field
	actionReject                // record a violation, keep the field
)

func (s *stripper) visit(
	prf protoreflect.Message,
	fd protoreflect.FieldDescriptor,
	depth int,
	checkFormats bool,
	violations *[]BehaviorViolation,
) error {
	got := s.resolve(fd)

	act, violation := s.decide(got, violations != nil)
	switch act {
	case actionClear:
		if violations == nil {
			prf.Clear(fd)
		} else {
			s.clears++
		}

		return nil
	case actionReject:
		if violations != nil {
			*violations = append(*violations, violation)
		}

		return nil
	case actionDescend:
	}

	if checkFormats && isStringValue(fd) {
		*violations = appendFormatViolations(*violations, s.formats.CheckField(prf, fd, s.segs.String()))
	}

	return s.recurse(prf, fd, depth+1, violations)
}

// isStringValue reports whether fd holds string values, the only values a
// field format applies to.
func isStringValue(fd protoreflect.FieldDescriptor) bool {
	if fd.IsMap() {
		return fd.MapValue().Kind() == protoreflect.StringKind
	}

	return fd.Kind() == protoreflect.StringKind
}

// decide returns the sanitization action for a populated field with annotations
// got and, when record is set, the strict-mode violation an actionReject records.
func (s *stripper) decide(got []annotations.FieldBehavior, record bool) (action, BehaviorViolation) {
	// Protected input is evidence for later planning, including when a caller
	// makes any containing message atomic. Validation never destroys it.
	if s.verify && protectedBehavior(got) != annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED {
		return actionDescend, BehaviorViolation{}
	}

	switch match := behavior.FirstMatch(got, s.opts.behaviors); match {
	case annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED, annotations.FieldBehavior_IMMUTABLE:
		return actionDescend, BehaviorViolation{}
	case annotations.FieldBehavior_OUTPUT_ONLY:
		return actionClear, BehaviorViolation{}
	default:
		if s.opts.strict {
			if !record {
				return actionReject, BehaviorViolation{}
			}

			return actionReject, BehaviorViolation{Path: s.segs.String(), Behavior: match, Reason: strictReason}
		}

		return actionClear, BehaviorViolation{}
	}
}

// strictReason explains a field [WithStrict] reports instead of clearing.
const strictReason = "field is not accepted in this message"

// protectedBehavior returns the update-protecting behavior of got: IDENTIFIER
// or IMMUTABLE, unless the field is OUTPUT_ONLY (cleared, never compared).
func protectedBehavior(got []annotations.FieldBehavior) annotations.FieldBehavior {
	if slices.Contains(got, annotations.FieldBehavior_OUTPUT_ONLY) {
		return annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED
	}

	return behavior.FirstMatch(got, []annotations.FieldBehavior{annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_IMMUTABLE})
}

func (s *stripper) recurse(
	prf protoreflect.Message,
	fd protoreflect.FieldDescriptor,
	depth int,
	violations *[]BehaviorViolation,
) error {
	// Skip subtrees whose descriptor tree carries no relevant annotation:
	// nothing below can match, so the walk (and Mutable materialization of
	// nested messages) is unnecessary. Message kind covers singular,
	// repeated, and map fields alike.
	if s.standard && fd.Kind() == protoreflect.MessageKind &&
		!s.subtreeRelevant(fd.Message()) {
		return nil
	}

	switch {
	case fd.IsList():
		if fd.Kind() == protoreflect.MessageKind {
			return s.recurseList(prf, fd, depth, violations)
		}
	case fd.IsMap():
		if fd.MapValue().Kind() == protoreflect.MessageKind {
			return s.recurseMap(prf, fd, depth, violations)
		}
	case fd.Kind() == protoreflect.MessageKind:
		return s.walk(s.fieldValue(prf, fd, violations).Message(), depth, violations)
	}

	return nil
}

// recurseList sanitizes populated elements; protection is checked by the merge evaluator.
func (s *stripper) recurseList(prf protoreflect.Message, fd protoreflect.FieldDescriptor, depth int, violations *[]BehaviorViolation) error {
	list := s.fieldValue(prf, fd, violations).List()
	for i := range list.Len() {
		s.segs.pushIndex(i)
		err := s.walk(list.Get(i).Message(), depth, violations)
		s.segs.pop()

		if err != nil {
			return err
		}
	}

	return nil
}

// recurseMap sanitizes populated entries in stable diagnostic order.
func (s *stripper) recurseMap(prf protoreflect.Message, fd protoreflect.FieldDescriptor, depth int, violations *[]BehaviorViolation) error {
	mm := s.fieldValue(prf, fd, violations).Map()
	for _, k := range behavior.SortedMapKeys(mm) {
		s.segs.pushKey(k)
		err := s.walk(mm.Get(k).Message(), depth, violations)
		s.segs.pop()

		if err != nil {
			return err
		}
	}

	return nil
}

// fieldValue returns a mutation-capable view of a populated message,
// repeated or map field when applying, or a read-only view when checking.
func (s *stripper) fieldValue(
	prf protoreflect.Message,
	fd protoreflect.FieldDescriptor,
	violations *[]BehaviorViolation,
) protoreflect.Value {
	if violations != nil {
		return prf.Get(fd)
	}

	return prf.Mutable(fd)
}
