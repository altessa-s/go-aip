// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldmask

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldmask/internal/maskwalk"
)

// Validate checks every path of the mask against the schema of msg by the
// path grammar (AIP-161), accepting the same spellings as
// [FieldMask.Resolve]. It returns a *[ValidationError] naming the first
// invalid path — malformed, unknown, an index into a repeated field, a field
// of repeated elements without "*", a map key that is not a valid value of
// the key type — or an error matching [ErrWildcardNotAlone] when "*" is
// combined with other paths. Validates schema only, not values. The options
// are those of [FieldMask.Resolve].
func (msk FieldMask) Validate(msg proto.Message, opts ...Option) error {
	if msg == nil || len(msk) == 0 {
		return nil
	}

	_, err := msk.resolve(msg.ProtoReflect().Descriptor(), newOptions(opts...))

	return err
}

// resolvedFor returns msk resolved against the schema of msg
// ([FieldMask.Resolve] under the options opts) and whether it is the
// full-replacement mask "*".
func (msk FieldMask) resolvedFor(msg proto.Message, opts []Option) (FieldMask, bool, error) {
	all, err := msk.SelectsAll()
	if err != nil || all {
		return nil, all, err
	}

	resolved, err := msk.resolve(msg.ProtoReflect().Descriptor(), newOptions(opts...))

	return resolved, false, err
}

// Filter keeps only fields present in the mask and clears all others.
// Handles nested messages, lists, and maps. An empty mask, or the
// full-replacement mask "*", keeps everything.
//
// The mask is validated first, as by [FieldMask.Validate], and applied by
// declared names, so a path may use any spelling [FieldMask.Resolve] accepts.
// An invalid path — an index into a repeated field ("authors.0") among
// them — is returned as an error and msg is left untouched.
//
// In dynamic JSON (by default, see [WithStrictDynamicPaths]) a path keeps
// exactly the addressed key ("extra_data.work.from"), "*" every key of a
// Struct value or every element of a list value ("extra_data.items.*.name").
// A path through a value of another shape — a scalar or null, a list without
// "*" — addresses nothing, and a read mask returns only what it addresses:
// the value is dropped (its key, or the field holding it) unless another
// path of the mask keeps it. Under "*" an element of a list addresses
// nothing when it is of another shape (a scalar under "items.*.name"): it is
// dropped and the other elements are kept in order, each with only its
// addressed keys, as Filter keeps the elements of a repeated message field.
// An addressed Struct is kept even when none of the addressed keys is
// present in it. The data is dynamic, so its shape is not an error.
// [FieldMask.Prune] removes what Filter keeps, and leaves a value of another
// shape as it is.
//
// Example:
//
//	err := mask.Filter(protoMessage) // Keep only specified fields
func (msk FieldMask) Filter(msg proto.Message, opts ...Option) error {
	if len(msk) == 0 || msg == nil {
		return nil
	}

	resolved, all, err := msk.resolvedFor(msg, opts)
	if err != nil || all {
		return err
	}

	maskwalk.Filter(resolved, msg, true)

	return nil
}

// Select keeps only the fields the mask selects, like [FieldMask.Filter],
// except for an empty mask: Filter treats it as "keep everything", Select as
// "keep nothing" and clears every field (and the unknown fields) of msg.
// Apply a mask computed with exclusions ([FromSetFields] with
// [WithSkipFields]) with Select or the update package's Merge, never with Filter: when
// every populated field is excluded the computed mask is empty, and Filter
// would keep the excluded fields. An invalid mask is an error, as from
// [FieldMask.Filter], and msg is left untouched.
func (msk FieldMask) Select(msg proto.Message, opts ...Option) error {
	if msg == nil {
		return nil
	}

	if len(msk) > 0 {
		return msk.Filter(msg, opts...)
	}

	clearMessage(msg)

	return nil
}

// clearMessage clears every field and the unknown fields of msg.
func clearMessage(msg proto.Message) {
	m := msg.ProtoReflect()
	if !m.IsValid() {
		return
	}

	var set []protoreflect.FieldDescriptor

	m.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		set = append(set, fd)
		return true
	})

	for _, fd := range set {
		m.Clear(fd)
	}

	m.SetUnknown(nil)
}

// Prune removes fields present in the mask while keeping others.
// Opposite of Filter. Handles nested messages, lists, and maps. An empty mask
// prunes nothing; the full-replacement mask "*" prunes everything.
//
// The mask is validated and resolved first, as by [FieldMask.Filter]; an
// invalid path is returned as an error and msg is left untouched.
//
// In dynamic JSON a path removes exactly the addressed key, "*" every key or
// element; a path through a value of another shape — a scalar or null, a
// list without "*", an element of another shape under "*" — addresses
// nothing and leaves that value as it is.
//
// Example:
//
//	err := mask.Prune(protoMessage) // Remove specified fields
func (msk FieldMask) Prune(msg proto.Message, opts ...Option) error {
	if len(msk) == 0 || msg == nil {
		return nil
	}

	resolved, all, err := msk.resolvedFor(msg, opts)
	if err != nil {
		return err
	}

	if all {
		clearMessage(msg)
		return nil
	}

	maskwalk.Prune(resolved, msg)

	return nil
}
