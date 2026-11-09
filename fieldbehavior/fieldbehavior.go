// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldbehavior

import (
	"slices"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/fieldinfo"
	"github.com/altessa-s/go-aip/internal/behavior"
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
//     equal value is cleared (the update ignores it), a different one is a
//     violation (INVALID_ARGUMENT). Without [WithCurrent] the stored value is
//     unknown, so a populated IMMUTABLE field is a violation: Strip never
//     silently discards a change it cannot verify.
//   - Every other behavior (IDENTIFIER, INPUT_ONLY, ...) is cleared, or
//     reported instead under [WithStrict].
//
// With [WithCurrent], msg is an update of current: every populated IDENTIFIER
// or IMMUTABLE field, in the set or not, is compared with current at the same
// path, and a different value is a violation. A path whose parent message
// current does not hold has no stored value, so any value there differs.
//
// Strip traverses singular sub-messages, repeated message elements, and map
// values. When a field itself matches, its subtree is handled as a whole and
// not descended. The elements of repeated and map fields have no stored
// counterpart: an update replaces a collection as a whole, so its elements
// are new values. IMMUTABLE and IDENTIFIER values inside them are therefore
// neither compared nor reported as unverified, and IMMUTABLE ones are kept;
// OUTPUT_ONLY and the other behaviors are handled as above.
//
// Under [WithFieldFormats] (on by default) the same walk also reports every
// string value it keeps that does not match the AIP-202 format of its field
// (google.api.field_info), with Behavior FIELD_BEHAVIOR_UNSPECIFIED; a field
// Strip clears or rejects is not checked.
//
// Strip checks before it mutates: when it returns *BehaviorViolationError
// (matching [ErrBehaviorViolation]) msg is left untouched, otherwise every
// clear has been applied. It returns [ErrMaxDepthExceeded] when [WithMaxDepth]
// is exceeded.
//
// Message types whose descriptor tree carries no relevant annotation are
// skipped without walking values, so [ErrMaxDepthExceeded] is only reported
// for subtrees that could actually match.
func Strip(msg proto.Message, opts ...Option) error {
	if msg == nil {
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
	if err := s.walk(prf, current, 0, false, &violations); err != nil {
		return err
	}

	if len(violations) > 0 {
		return &BehaviorViolationError{Violations: violations}
	}

	// The check pass found nothing to clear: the apply pass would be a no-op.
	if s.clears == 0 {
		return nil
	}

	return s.walk(prf, current, 0, false, nil)
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

// StripUpdate sanitizes the resource of an Update request whose populated
// fields are the update (an update without a field mask, or a full
// replacement): OUTPUT_ONLY fields are cleared at every depth, and IMMUTABLE
// fields are compared with the stored resource passed with [WithCurrent]: an
// unchanged value is cleared, a changed one is a violation (AIP-203). Without
// [WithCurrent] a populated IMMUTABLE field is a violation, because the
// change cannot be ruled out. IDENTIFIER fields are kept: they address the
// resource being updated; with [WithCurrent] a populated IDENTIFIER that
// differs from the stored one is a violation too.
//
// An update driven by a field mask needs no StripUpdate:
// [github.com/altessa-s/go-aip/fieldmask/update.Apply] with its
// WithCurrent option applies the same rules to the fields the mask selects
// and ignores the others.
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

// walk visits every field of prf. cur is the stored counterpart of prf (nil
// when it has none or no current value was supplied) and inCollection reports
// that prf lies inside a repeated or map element. With violations non-nil the
// walk only checks and records; with violations nil it applies the clears.
func (s *stripper) walk(
	prf, cur protoreflect.Message,
	depth int,
	inCollection bool,
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
		err := s.visit(prf, cur, fd, depth, inCollection, checkFormats, violations)
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
	prf, cur protoreflect.Message,
	fd protoreflect.FieldDescriptor,
	depth int,
	inCollection bool,
	checkFormats bool,
	violations *[]BehaviorViolation,
) error {
	act, violation := s.decide(s.resolve(fd), prf, cur, fd, inCollection, violations != nil)
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

	return s.recurse(prf, cur, fd, depth+1, inCollection, violations)
}

// isStringValue reports whether fd holds string values, the only values a
// field format applies to.
func isStringValue(fd protoreflect.FieldDescriptor) bool {
	if fd.IsMap() {
		return fd.MapValue().Kind() == protoreflect.StringKind
	}

	return fd.Kind() == protoreflect.StringKind
}

// decide returns the action for the populated field fd of prf, whose
// annotations are got, and, when record is set, the violation an actionReject
// records.
func (s *stripper) decide(
	got []annotations.FieldBehavior,
	prf, cur protoreflect.Message,
	fd protoreflect.FieldDescriptor,
	inCollection bool,
	record bool,
) (action, BehaviorViolation) {
	// A stored IDENTIFIER or IMMUTABLE value outside collections must not
	// change, whether or not the set names the behavior.
	if s.verify && !inCollection {
		if b := protectedBehavior(got); b != annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED && !behavior.SameValue(prf, cur, fd) {
			return actionReject, s.protectedViolation(b, true, record)
		}
	}

	switch match := behavior.FirstMatch(got, s.opts.behaviors); match {
	case annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED:
		return actionDescend, BehaviorViolation{}
	case annotations.FieldBehavior_OUTPUT_ONLY:
		return actionClear, BehaviorViolation{}
	case annotations.FieldBehavior_IMMUTABLE:
		switch {
		case inCollection:
			// A new element's value: kept, its subtree still sanitized.
			return actionDescend, BehaviorViolation{}
		case s.verify:
			// Unchanged: a different value was rejected above.
			return actionClear, BehaviorViolation{}
		default:
			return actionReject, s.protectedViolation(match, false, record)
		}
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

// protectedViolation builds the violation of a changed (or unverifiable)
// protected value at the current path; nothing when record is unset.
func (s *stripper) protectedViolation(b annotations.FieldBehavior, verified, record bool) BehaviorViolation {
	if !record {
		return BehaviorViolation{}
	}

	return behavior.ProtectedViolation(s.segs.String(), b, verified)
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
	prf, cur protoreflect.Message,
	fd protoreflect.FieldDescriptor,
	depth int,
	inCollection bool,
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
		if fd.Kind() != protoreflect.MessageKind {
			return nil
		}

		list := s.fieldValue(prf, fd, violations).List()

		for i := range list.Len() {
			s.segs.pushIndex(i)
			err := s.walk(list.Get(i).Message(), nil, depth, true, violations)
			s.segs.pop()

			if err != nil {
				return err
			}
		}

		return nil

	case fd.IsMap():
		if fd.MapValue().Kind() != protoreflect.MessageKind {
			return nil
		}

		mm := s.fieldValue(prf, fd, violations).Map()
		if violations == nil {
			// Applying: the check pass already reported in key order and
			// clearing inside a value never adds or removes entries, so the
			// entries are visited unordered.
			var err error

			mm.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
				s.segs.pushKey(k)
				err = s.walk(v.Message(), nil, depth, true, nil)
				s.segs.pop()

				return err == nil
			})

			return err
		}

		for _, k := range behavior.SortedMapKeys(mm) {
			s.segs.pushKey(k)
			err := s.walk(mm.Get(k).Message(), nil, depth, true, violations)
			s.segs.pop()

			if err != nil {
				return err
			}
		}

		return nil

	case fd.Kind() == protoreflect.MessageKind:
		return s.walk(s.fieldValue(prf, fd, violations).Message(), behavior.Child(cur, fd), depth, inCollection, violations)
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
