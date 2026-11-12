// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/maskmerge"
	"github.com/altessa-s/go-aip/internal/maskpath"
	"github.com/altessa-s/go-aip/internal/maskwalk"
)

// ErrMessageMismatch is returned by [Merge] for messages that are
// nil (or typed-nil), not of one descriptor, or cannot be converted into the
// destination representation. Conversion errors retain their underlying cause.
var ErrMessageMismatch = errors.New("fieldmask: messages are nil or of different types")

// ErrElementCountMismatch reports a "*" sub-path of a repeated field
// ("aliases.*.display_name") whose source list holds a different number of
// elements than the destination list: the elements are paired by index, so
// the update cannot tell which element a value belongs to.
var ErrElementCountMismatch = maskmerge.ErrElementCountMismatch

// Merge copies the fields msk selects from src to dst, both messages of the
// same descriptor, as [MergePaths] copies them: msk may name top-level fields,
// nested fields, map keys and "*" on repeated and map fields, in any spelling
// of its field segments. A selected field src does not populate is cleared
// in dst, fields outside the mask are kept, and dst never shares memory with
// src.
//
// The mask means exactly what it names. An empty mask changes nothing; for
// the fields an update implies when its mask is omitted, build the mask from
// src first:
//
//	err := fieldmask.FromSetFields[fieldmask.FieldMask](src, fieldmask.WithSkipFields("id")).Merge(dst, src)
//
// "*" alone ([fieldmask.FieldMask.SelectsAll]) replaces every top-level field and "*"
// with other paths is [fieldmask.ErrWildcardNotAlone]. A path that is not an update
// path ([ResolvePath] under the options, of which
// [WithStrictDynamicPaths] and the limits apply) is a *[fieldmask.ValidationError].
// Nil (including typed-nil) messages, or messages not of one descriptor,
// are [ErrMessageMismatch]. Generated and dynamic representations of the same
// descriptor may be mixed. Every check runs before the first
// write, so dst is changed all-or-nothing.
func Merge(msk fieldmask.FieldMask, dst, src proto.Message, opts ...Option) error {
	d, err := reflectPair(dst, src)
	if err != nil {
		return err
	}

	all, err := msk.SelectsAll()
	if err != nil {
		return err
	}

	if !all {
		return mergePaths(dst, src, msk.ToPaths(), newOptions(opts...))
	}

	fields := d.Descriptor().Fields()

	source, err := cloneMessage(src)
	if err != nil {
		return err
	}

	work, err := cloneMessage(dst)
	if err != nil {
		return err
	}

	for i := range fields.Len() {
		if err := maskmerge.CopyField(work.ProtoReflect(), source.ProtoReflect(), fields.Get(i)); err != nil {
			return conversionError(err)
		}
	}

	return publishMessage(dst, work)
}

// MergePaths copies the fields paths name from src to dst, messages of one
// descriptor: the merge of a [Plan]. Each path is an update path
// ([ResolvePath]: declared, JSON or snake_case field segments, map keys
// and "*" on repeated and map fields):
//   - a field path replaces the whole field (a parent path replaces the
//     block, a collection path or a trailing "*" the whole collection), and a
//     nested path only that field, keeping its siblings;
//   - a map key path ("labels.k") replaces that entry, or deletes it when src
//     does not hold it; a key of a google.protobuf.Struct ("attrs.color") is
//     such a map key;
//   - by default (not with [WithStrictDynamicPaths]) a path goes on into the
//     dynamic JSON below a Struct key, a google.protobuf.Value or through
//     "*" of a ListValue: a nested key ("attrs.work.from") is replaced from
//     src or deleted when src lacks it, "*" over a Struct value addresses
//     every key src holds, and a missing or non-Struct value on the way is
//     replaced by a new Struct only when src holds a value to write there;
//     "*" on a list value ("attrs.items.*.name") updates the elements in
//     place by index under the count rule of a repeated field below;
//   - a sub-path through a map key ("labels.k.display_name") updates that
//     entry in place, creating it when only src holds it; through "*" on a
//     map ("labels.*.display_name") it updates every entry src holds that no
//     other path names by key, and leaves the entries only dst holds alone;
//   - a sub-path through "*" on a repeated field ("aliases.*.display_name")
//     updates every element of dst in place from the src element of the same
//     index; src must hold as many elements as dst, or none (the field is
//     then cleared in every element), else the merge is refused with
//     [ErrElementCountMismatch].
//
// A named field src does not populate is cleared in dst; a nested path whose
// parent is set in neither message changes nothing (no empty parent or entry
// is created). Values are deep copies, so dst never shares memory with src.
// Every path and element count is checked before the first write
// (*[fieldmask.ValidationError] for a bad path, [ErrMessageMismatch] for nil or descriptor-mismatched
// messages, [ErrElementCountMismatch]), so dst is changed all-or-nothing; no
// paths change nothing. For a dynamic JSON root (Struct, Value or ListValue),
// "*" alone replaces the entire root, including clearing absent object keys or
// changing a list's length; protobuf wrapper names are never used as JSON paths.
// Fields are written in declaration order and map keys
// in sorted order, so two paths setting members of one oneof leave the later
// declared member. The mask limits of the options ([WithMaxPaths],
// [WithMaxPathLength], [WithMaxPathDepth]) are checked before anything else.
func MergePaths(dst, src proto.Message, paths []string, opts ...Option) error {
	return mergePaths(dst, src, paths, newOptions(opts...))
}

func mergePaths(dst, src proto.Message, paths []string, policy *options) error {
	d, err := reflectPair(dst, src)
	if err != nil {
		return err
	}

	if err = policy.validateMaskLimits(paths); err != nil {
		return err
	}

	md := d.Descriptor()

	tree, err := mergeSelection(md, paths, policy)
	if err != nil {
		return err
	}

	if len(tree) == 0 {
		return nil
	}

	source, err := cloneMessage(src)
	if err != nil {
		return err
	}

	work, err := cloneMessage(dst)
	if err != nil {
		return err
	}

	if err := maskmerge.CheckElementCounts(work.ProtoReflect(), source.ProtoReflect(), tree, md, ""); err != nil {
		return &elementCountError{Path: err.Path, Reason: err.Reason}
	}

	if err := maskmerge.Tree(work.ProtoReflect(), source.ProtoReflect(), tree, md); err != nil {
		return conversionError(err)
	}

	return publishMessage(dst, work)
}

// reflectPair validates both messages and returns the destination. Equal full
// names are not enough: independently built descriptors cannot be mixed.
// The writer converts nested values into the destination's Go representation.
func reflectPair(dst, src proto.Message) (protoreflect.Message, error) {
	if dst == nil || src == nil {
		return nil, ErrMessageMismatch
	}

	if nilcheck.IsNil(dst) || nilcheck.IsNil(src) {
		return nil, fmt.Errorf("%w: nil message", ErrMessageMismatch)
	}

	d, s := dst.ProtoReflect(), src.ProtoReflect()
	if !d.IsValid() || !s.IsValid() {
		return nil, fmt.Errorf("%w: nil message", ErrMessageMismatch)
	}

	if d.Descriptor() != s.Descriptor() {
		return nil, fmt.Errorf("%w: %T (%s) and %T (%s)", ErrMessageMismatch,
			dst, d.Descriptor().FullName(), src, s.Descriptor().FullName())
	}

	return d, nil
}

// validMessage rejects typed nils before reflection: unlike generated messages,
// a nil dynamicpb.Message cannot safely report its validity or descriptor.
func validMessage(msg proto.Message) bool {
	return !nilcheck.IsNil(msg) && msg.ProtoReflect().IsValid()
}

// mergeSelection builds the exact tree consumed by the real merge and by
// protected comparison. Resolve before removing OUTPUT_ONLY selections, so
// trailing collection wildcards collapse and explicit keys remain exclusions.
func mergeSelection(md protoreflect.MessageDescriptor, paths []string, policy *options) (fieldmask.FieldMask, error) {
	if maskpath.IsDynamicMessage(md) {
		all, err := selectsAll(paths)
		if err != nil {
			// MergePaths reports path validation, never a planner's indexed
			// mask error, including at a dynamic root.
			mp, _ := errors.AsType[*MaskPathError](err)
			return nil, &fieldmask.ValidationError{Path: mp.Path, Reason: "not an update path of " + string(md.FullName()) + ": " + mp.reason}
		}

		if all {
			return fieldmask.FieldMask{maskpath.Wildcard: nil}, nil
		}
	}

	tree := fieldmask.FieldMask{}

	for _, p := range paths {
		segs, reason := resolveUpdateSegments(md, p, policy)
		if reason != "" {
			return nil, &fieldmask.ValidationError{Path: p, Reason: "not an update path of " + string(md.FullName()) + ": " + reason}
		}

		maskwalk.Insert(tree, segs)
	}

	return tree, nil
}

// elementCountError preserves the public merge refusal's base error shape.
type elementCountError struct {
	Path   string
	Reason string
}

func (e *elementCountError) Error() string {
	return fmt.Sprintf("fieldmask: path %q: %s", e.Path, e.Reason)
}

func (e *elementCountError) Unwrap() error { return ErrElementCountMismatch }

// cloneCurrent preserves pair-validation errors before making a private copy.
func cloneCurrent(in, current proto.Message) (proto.Message, error) {
	if _, err := reflectPair(in, current); err != nil {
		return nil, err
	}

	return cloneMessage(current)
}

// cloneMessage converts every child into its parent's representation. proto.Clone
// assumes descriptor identity even for children accepted by dynamicpb by name.
func cloneMessage(msg proto.Message) (proto.Message, error) {
	if !validMessage(msg) {
		return nil, ErrMessageMismatch
	}

	out := msg.ProtoReflect().New().Interface()
	if err := publishMessage(out, msg); err != nil {
		return nil, err
	}

	return out, nil
}

// publishMessage completes all conversions before replacing the live message.
func publishMessage(dst, src proto.Message) error {
	prepared, err := preparePublication(dst, src)
	if err != nil {
		return err
	}

	commitMessage(dst, prepared)

	return nil
}

// conversionError keeps the existing mismatch identity for a representation
// that cannot carry a value, and retains the protobuf cause for diagnostics.
func conversionError(err error) error {
	if err == nil || errors.Is(err, ErrMessageMismatch) || errors.Is(err, ErrElementCountMismatch) {
		return err
	}

	return fmt.Errorf("%w: converting message: %w", ErrMessageMismatch, err)
}

// preparePublication allocates using the live destination's representation.
// The returned message owns every value and requires no further conversion.
func preparePublication(dst, src proto.Message) (proto.Message, error) {
	prepared := dst.ProtoReflect().New()
	if err := maskmerge.ReplaceMessage(prepared, src.ProtoReflect()); err != nil {
		return nil, conversionError(err)
	}

	return prepared.Interface(), nil
}

func commitMessage(dst, prepared proto.Message) {
	proto.Reset(dst)
	prepared.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		dst.ProtoReflect().Set(fd, value)
		return true
	})
	dst.ProtoReflect().SetUnknown(prepared.ProtoReflect().GetUnknown())
}
