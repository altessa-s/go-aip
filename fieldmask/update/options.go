// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

//go:generate optgen generate --type=options --option-type=Option --output=options_gen.go

import (
	"errors"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskpath"
	"github.com/altessa-s/go-aip/internal/behavior"
)

// options configures update planning and path traversal: the mask policy
// [NewSpec] captures and the merge, synthesis and path helpers share, and the
// per-request values of [Apply], [ValidateStrict] and [Spec.Plan].
type options struct {
	// WithResolver replaces annotation lookup for update planning, including nested fields. Nil uses annotations.
	resolver            fieldbehavior.Resolver                    `optgen:"default=fieldbehavior.AnnotationResolver{}"`
	identityFields      []string                                  `optgen:"default=[]string{fieldID, fieldEtag},allownull"`
	impliedSkipFields   []string                                  `optgen:"allownull"`
	immutableFields     []string                                  `optgen:"allownull"`
	atomicMessage       func(protoreflect.MessageDescriptor) bool `optgen:"default=DefaultAtomicMessage"`
	updateMaskFieldName string                                    `optgen:"default=DefaultUpdateMaskField"`
	// WithStrictDynamicPaths limits the paths into google.protobuf.Struct, Value and ListValue fields to the
	// AIP-161 rule: a Struct field whole, "*" or one key, and nothing deeper; a Value or ListValue whole only. By
	// default a path walks the dynamic JSON below a Struct key (an extension beyond AIP-161, see the path grammar).
	// Strict deep paths are rejected in every PathValidationMode.
	strictDynamicPaths bool `optval:"param"`
	// WithMaxPathDepth bounds the segments of a path ("*" and quoted keys included). Zero is unlimited; a negative
	// value keeps the default.
	maxPathDepth int `optgen:"default=fieldmask.DefaultMaxPathDepth" optval:"positive=allow_zero"`
	// WithMaxPaths bounds the paths of a mask. Zero is unlimited; a negative value keeps the default.
	maxPaths int `optgen:"default=fieldmask.DefaultMaxPaths" optval:"positive=allow_zero"`
	// WithMaxPathLength bounds the bytes of a raw path. Zero is unlimited; a negative value keeps the default.
	maxPathLength int `optgen:"default=fieldmask.DefaultMaxPathLength" optval:"positive=allow_zero"`

	// current is the stored resource the update applies to, a message of the
	// request resource's type. With it an IMMUTABLE or IDENTIFIER value equal
	// to the stored one is ignored and a different one is a violation
	// (AIP-203); without it (nil) the stored value is unknown and every
	// IMMUTABLE or IDENTIFIER value the update would write is a violation.
	current proto.Message `optgen:"allownull"`

	pathValidation PathValidationMode
	pathReporter   func(*fieldmask.ValidationError) `opt:"PathValidationReporter"`

	// WithEmptyMaskImplied makes [ApplyMask] apply the implied mask of the populated fields ([FromSetFields]) to an update_mask
	// sent with no paths, as to an omitted one, instead of treating it as a no-op (AIP-134). It is decided on the mask as sent: a
	// mask whose paths are all dropped is never implied. Only ApplyMask reads it.
	emptyMaskImplied bool `optval:"param"`
	// WithStrict makes [ApplyMask] check the mask as [ValidateStrict] does: every path is enforced whatever [WithPathValidation]
	// says, and every refusal is a *[StrictError] matching [ErrInvalidUpdateMask]. Only ApplyMask reads it.
	strict bool `optval:"param"`
}

// behaviors returns the annotations of fd. The default resolver's cached
// slice is returned without a copy: callers must not modify the result.
func (o *options) behaviors(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
	if nilcheck.IsNil(o.resolver) {
		return behavior.Shared(fd)
	}

	if _, standard := o.resolver.(fieldbehavior.AnnotationResolver); standard {
		return behavior.Shared(fd)
	}

	return o.resolver.Resolve(fd)
}

func (o *options) serverManaged(fd protoreflect.FieldDescriptor) annotations.FieldBehavior {
	return behavior.FirstMatch(o.behaviors(fd), []annotations.FieldBehavior{
		annotations.FieldBehavior_OUTPUT_ONLY, annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_IMMUTABLE,
	})
}

func (o *options) expandable(fd protoreflect.FieldDescriptor) bool {
	return isExpandableMessage(fd) && (o.atomicMessage == nil || !o.atomicMessage(fd.Message()))
}

// checkAtomicMask prevents a partial mutation from descending into an atomic value.
func (o *options) checkAtomicMask(msk fieldmask.FieldMask, md protoreflect.MessageDescriptor, prefix string) error {
	for name, nested := range msk {
		if nested == nil {
			continue
		}

		fd := md.Fields().ByName(protoreflect.Name(name))
		if fd == nil {
			continue
		}

		child := fd.Message()
		if fd.IsMap() {
			child = fd.MapValue().Message()
		}

		if child == nil {
			continue
		}

		path := behavior.JoinPath(prefix, name)
		if o.atomicMessage != nil && o.atomicMessage(child) {
			return &fieldmask.ValidationError{Path: path, Reason: reasonAtomicWhole}
		}

		if maskpath.IsDynamicMessage(child) {
			continue // dynamic JSON: keys, not fields
		}

		if fd.IsList() || fd.IsMap() {
			for key, sub := range elementMasks(fd, nested) {
				if err := o.checkAtomicMask(sub, child, elementPath(path, key)); err != nil {
					return err
				}
			}
		} else if err := o.checkAtomicMask(nested, child, path); err != nil {
			return err
		}
	}

	return nil
}

// reasonAtomicWhole is the reason of a path into an atomic message.
const reasonAtomicWhole = "atomic message must be selected as a whole"

// rejectAtomicDescent returns the *fieldmask.ValidationError of raw, a path
// the schema rejects, when its fields walk into a message the atomic policy
// keeps whole ([WithAtomicMessage]) and more segments follow, whatever they
// are ("delete_time.typo", "deleteTime.*"): such a path is refused in every
// [PathValidationMode], as [options.checkAtomicMask] refuses a valid one,
// rather than dropped. It returns nil for any other path.
func (o *options) rejectAtomicDescent(md protoreflect.MessageDescriptor, raw string) error {
	segs, err := maskpath.Parse(raw)
	if err != nil || o.atomicMessage == nil {
		return nil
	}

	lookup := maskpath.Check{Aliases: true}
	out := make([]string, 0, len(segs))

	for i := 0; i < len(segs)-1; i++ {
		if maskpath.IsDynamicMessage(md) || maskpath.IsWildcard(segs[i]) {
			return nil
		}

		fd := lookup.Lookup(md, segs[i].Text)
		if fd == nil {
			return nil
		}

		out = append(out, string(fd.Name()))
		child := fd.Message()

		if fd.IsList() || fd.IsMap() {
			i++ // the element key or "*"
			if i == len(segs)-1 {
				return nil
			}

			out = append(out, segs[i].Text)
			child = maskpath.ValueMessage(fd)
		}

		switch {
		case child == nil:
			return nil
		case o.atomicMessage(child):
			return &fieldmask.ValidationError{Path: maskpath.Join(out), Reason: reasonAtomicWhole}
		}

		md = child
	}

	return nil
}

// limits returns the mask limits of o.
func (o *options) limits() maskpath.Limits {
	return maskpath.Limits{MaxPaths: o.maxPaths, MaxPathLength: o.maxPathLength, MaxPathDepth: o.maxPathDepth}
}

// validateMaskLimits reports the first limit paths break as a
// *fieldmask.ValidationError (none for the path count), or nil.
func (o *options) validateMaskLimits(paths []string) error {
	if v := o.limits().CheckMask(paths); v != nil {
		return &fieldmask.ValidationError{Path: v.Path, Reason: v.Reason, Cause: v.Cause}
	}

	return nil
}

// limitFieldError is the limit violation v as the field error of
// [Spec.Plan]: on the offending path, or on the bare mask field for the path
// count.
func limitFieldError(v *maskpath.Violation) error {
	return indexedFieldError(v.Cause, v.Index, v.Path, "", v.Reason)
}

// validationError returns err, an error of the path grammar, as the
// *fieldmask.ValidationError of the path it names; any other error as it is.
func validationError(err error) error {
	if e, ok := errors.AsType[*maskpath.Error](err); ok {
		return &fieldmask.ValidationError{Path: e.Path, Reason: e.Reason}
	}

	return err
}
