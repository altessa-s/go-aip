// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldmask

import (
	"google.golang.org/protobuf/proto"

	"github.com/altessa-s/go-aip/internal/maskpath"
)

// FromMessage creates a FieldMask or []string for all fields defined in the message schema.
// Returns all paths regardless of whether fields are set.
func FromMessage[T interface{ []string | FieldMask }](msg proto.Message) T {
	return fromMessage[T](msg, false, defaultSetFieldsOptions(), DefaultMaxPathDepth)
}

// FromSetFields creates a FieldMask or []string for only set (non-nil/non-zero) fields.
// Recursively traverses nested structures. Field presence follows protoreflect: zero
// scalars, false, empty lists and maps and unset messages are not set, and a set oneof
// contributes its set field.
//
// The options shape the result: [WithTopLevelOnly] keeps the top-level fields only (the
// fields an update replaces as a whole), [WithSkipFields] drops fields such as the
// identity or the etag, and [WithDeclarationOrder] orders a []string result by field
// declaration. Together they give the mask an update implies when the request leaves it
// empty:
//
//	paths := fieldmask.FromSetFields[[]string](resource,
//	    fieldmask.WithTopLevelOnly(true), fieldmask.WithDeclarationOrder(true), fieldmask.WithSkipFields("id", "etag"))
//
// A FieldMask result keeps a parent and drops its sub-paths, so without a nested skip it
// holds the top-level fields even without [WithTopLevelOnly].
//
// A google.protobuf.Struct contributes its top-level keys only ("extra_data.color", never
// "extra_data.color.hue"), and a Value or ListValue the field alone, so the paths are
// stable and valid with and without [WithStrictDynamicPaths].
//
// A nested skip ("profile.secret") never lets the skipped field in through a parent that
// covers it: the parent's own path is dropped and its other set sub-paths are kept, so the
// mask selects "profile.display_name" instead of all of "profile". Where that cannot be
// expressed — with [WithTopLevelOnly], or when the path runs through a repeated, map or
// google.protobuf.Struct/ListValue/Value field — the whole top-level field holding the
// skipped one is left out instead. A skip naming no field of the schema drops nothing.
//
// A mask computed with skips can be empty (every populated field was skipped). Apply it
// with [FieldMask.Select] or the update package's Merge, which treat an empty mask as selecting
// nothing; [FieldMask.Filter] treats an empty mask as keeping everything and would keep
// the skipped fields.
func FromSetFields[T interface{ []string | FieldMask }](msg proto.Message, opts ...SetFieldsOption) T {
	return fromMessage[T](msg, true, newSetFieldsOptions(opts...), DefaultMaxPathDepth)
}

// fromMessage is [FromMessage] or, with onlySet, [FromSetFields], with paths
// of at most maxDepth segments (zero is unlimited).
func fromMessage[T interface{ []string | FieldMask }](msg proto.Message, onlySet bool, o *setFieldsOptions, maxDepth int) T {
	var zero T
	if msg == nil {
		return zero
	}

	paths := maskpath.Collect(msg.ProtoReflect(), onlySet, maskpath.SetFields{
		TopLevelOnly: o.topLevelOnly, DeclarationOrder: o.declarationOrder, Skip: o.skipFields,
	}, maxDepth)

	// The constraint admits only []string or FieldMask; pick the branch by
	// inspecting the zero value's dynamic type.
	switch any(zero).(type) {
	case []string:
		return any(paths).(T) //nolint:errcheck // covered by the type constraint.
	case FieldMask:
		return any(FromPaths(paths...)).(T) //nolint:errcheck // covered by the type constraint.
	}

	return zero
}
