// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldmask_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	testpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// structPathCase is a mask path into a google.protobuf.Struct, Value or
// ListValue field with its resolution by every entry point by default:
// resolved is the [fieldmask.FieldMask.Resolve] spelling and update the
// [update.ResolvePath] spelling ("" for both when rejected). strict
// reports that [fieldmask.WithStrictDynamicPaths] accepts it too, and
// structural that a rejection by default holds even with
// PathValidationDisabled.
type structPathCase struct {
	path       string
	resolved   string
	update     string
	strict     bool
	structural bool
}

// structPathCases are the dynamic paths of UpdateRequest: extra_data is a
// Struct, struct_map a map<string, Struct>, struct_list a repeated Struct,
// value_data a Value and list_data a ListValue.
var structPathCases = []structPathCase{
	{path: "extra_data", resolved: "extra_data", update: "extra_data", strict: true},
	{path: "extraData", resolved: "extra_data", update: "extra_data", strict: true},
	{path: "extra_data.*", resolved: "extra_data.*", update: "extra_data", strict: true},
	{path: "extra_data.color", resolved: "extra_data.color", update: "extra_data.color", strict: true},
	{path: "extra_data.0", resolved: "extra_data.0", update: "extra_data.0", strict: true},
	{path: "extraData.`a.b`", resolved: "extra_data.`a.b`", update: "extra_data.`a.b`", strict: true},
	{path: "extra_data.`x``y`", resolved: "extra_data.`x``y`", update: "extra_data.`x``y`", strict: true},
	{path: "extra_data.``", resolved: "extra_data.``", update: "extra_data.``", strict: true},
	{path: "struct_map.k", resolved: "struct_map.k", update: "struct_map.k", strict: true},
	{path: "struct_map.k.color", resolved: "struct_map.k.color", update: "struct_map.k.color", strict: true},
	{path: "struct_map.*.color", resolved: "struct_map.*.color", update: "struct_map.*.color", strict: true},
	{path: "struct_list.*", resolved: "struct_list.*", update: "struct_list", strict: true},
	{path: "struct_list.*.color", resolved: "struct_list.*.color", update: "struct_list.*.color", strict: true},
	{path: "value_data", resolved: "value_data", update: "value_data", strict: true},
	{path: "list_data", resolved: "list_data", update: "list_data", strict: true},

	{path: "extra_data.color.hue", resolved: "extra_data.color.hue", update: "extra_data.color.hue", structural: true},
	{path: "extraData.`a.b`.c", resolved: "extra_data.`a.b`.c", update: "extra_data.`a.b`.c", structural: true},
	{path: "extra_data.*.hue", resolved: "extra_data.*.hue", update: "extra_data.*.hue", structural: true},
	{path: "extra_data.color.*", resolved: "extra_data.color.*", update: "extra_data.color", structural: true},
	{path: "extra_data.items.*.name", resolved: "extra_data.items.*.name", update: "extra_data.items.*.name", structural: true},
	{path: "extra_data.items.0", resolved: "extra_data.items.0", update: "extra_data.items.0", structural: true},
	{path: "struct_map.k.color.hue", resolved: "struct_map.k.color.hue", update: "struct_map.k.color.hue", structural: true},
	{path: "struct_map.*.*.hue", resolved: "struct_map.*.*.hue", update: "struct_map.*.*.hue", structural: true},
	{path: "struct_list.*.color.hue", resolved: "struct_list.*.color.hue", update: "struct_list.*.color.hue", structural: true},
	{path: "value_data.color", resolved: "value_data.color", update: "value_data.color", structural: true},
	{path: "value_data.*", resolved: "value_data.*", update: "value_data", structural: true},
	{path: "list_data.*", resolved: "list_data.*", update: "list_data", structural: true},
	{path: "list_data.*.name", resolved: "list_data.*.name", update: "list_data.*.name", structural: true},

	{path: "list_data.0", structural: true},
	{path: "list_data.0.name", structural: true},
	{path: "list_data.name"},
}

// TestStructPathsResolveAlikeInEveryEntryPoint pins that every entry point
// reading a mask accepts and rejects the same dynamic paths, by default and
// with WithStrictDynamicPaths (AIP-161: one meaning of a mask across
// methods).
func TestStructPathsResolveAlikeInEveryEntryPoint(t *testing.T) {
	t.Parallel()

	md := (&testpb.UpdateRequest{}).ProtoReflect().Descriptor()

	for _, strict := range []bool{false, true} {
		read := []fieldmask.Option{fieldmask.WithStrictDynamicPaths(strict)}
		policy := []update.Option{update.WithStrictDynamicPaths(strict)}

		spec, err := update.NewSpec(&testpb.UpdateRequest{}, policy...)
		require.NoError(t, err)

		for _, tc := range structPathCases {
			t.Run(fmt.Sprintf("strict=%t/%s", strict, tc.path), func(t *testing.T) {
				t.Parallel()

				accepted := tc.resolved != "" && (tc.strict || !strict)
				resolved, updatePath := tc.resolved, tc.update

				if !accepted {
					resolved, updatePath = "", ""
				}

				got, err := fieldmask.FromPaths(tc.path).Resolve(md, read...)
				if accepted {
					require.NoError(t, err)
					require.Equal(t, []string{resolved}, got.ToPaths())
				} else {
					requireInvalidPath(t, err, tc.path)
				}

				require.Equal(t, resolved, fieldmask.ResolvePath(md, tc.path, read...))
				require.Equal(t, accepted, fieldmask.FromPaths(tc.path).Validate(&testpb.UpdateRequest{}, read...) == nil, "Validate")
				require.Equal(t, accepted, fieldmask.FromPaths(tc.path).Filter(structRequest(), read...) == nil, "Filter")
				require.Equal(t, accepted, fieldmask.FromPaths(tc.path).Prune(structRequest(), read...) == nil, "Prune")
				require.Equal(t, updatePath, update.ResolvePath(md, tc.path, policy...), "ResolveUpdatePath")

				for _, mode := range []update.PathValidationMode{update.PathValidationEnforce, update.PathValidationDisabled} {
					// With strict dynamic paths every rejection here is structural.
					want := accepted || (mode == update.PathValidationDisabled && !tc.structural && !strict)
					err := update.Apply(fieldmask.FromPaths(tc.path), structRequest(),
						append([]update.Option{update.WithPathValidation(mode)}, policy...)...)
					require.Equal(t, want, err == nil, "ApplyUpdateMask mode %d: %v", mode, err)
				}

				_, err = update.ValidateStrict(fieldmask.FromPaths(tc.path), structRequest(), policy...)
				require.Equal(t, accepted, err == nil, "ValidateStrictUpdateMask: %v", err)

				err = update.MergePaths(structRequest(), structRequest(), []string{tc.path}, policy...)
				requireAcceptedOrInvalid(t, accepted, err, "MergePaths")

				err = update.Merge(fieldmask.FromPaths(tc.path), structRequest(), structRequest(), policy...)
				requireAcceptedOrInvalid(t, accepted, err, "Merge")

				plan, err := spec.Plan(structRequest(), &fieldmaskpb.FieldMask{Paths: []string{"description", tc.path}})
				if accepted {
					require.NoError(t, err)
					require.Contains(t, plan.Paths, updatePath)

					return
				}

				requirePlanPathError(t, err, 1, tc.path)
			})
		}
	}
}

// TestStructLiteralWildcardKey pins that quoted JSON keys survive resolution
// and mask serialization without becoming a wildcard.
func TestStructLiteralWildcardKey(t *testing.T) {
	t.Parallel()

	md := (&testpb.UpdateRequest{}).ProtoReflect().Descriptor()

	spec, err := update.NewSpec(&testpb.UpdateRequest{})
	require.NoError(t, err)

	for _, raw := range []string{"extra_data.`*`", "extra_data.color.`*`", "value_data.`*`"} {
		require.Equal(t, raw, fieldmask.ResolvePath(md, raw), raw)
		require.Equal(t, raw, update.ResolvePath(md, raw), raw)
		require.NoError(t, update.MergePaths(structRequest(), structRequest(), []string{raw}), raw)

		_, err = spec.Plan(structRequest(), &fieldmaskpb.FieldMask{Paths: []string{"description", raw}})
		require.NoError(t, err, raw)
		require.Equal(t, []string{raw}, fieldmask.FromPaths(raw).ToPaths())
	}
}

// TestStructEntriesMergeLikeMapEntries pins the AIP-134 semantics of Struct
// entries in MergePaths, Merge, Plan and ApplyUpdateMask: a key replaces its
// entry from the request or deletes it when the request lacks it, and the
// field or "*" replaces the whole Struct.
func TestStructEntriesMergeLikeMapEntries(t *testing.T) {
	t.Parallel()

	stored := func() *testpb.UpdateRequest {
		return &testpb.UpdateRequest{
			ExtraData: newTestStruct(map[string]any{"color": "red", "size": 1.0, "a.b": "dotted", "keep": true}),
			StructMap: map[string]*structpb.Struct{"k": newTestStruct(map[string]any{"color": "red", "size": 1.0})},
		}
	}
	request := func() *testpb.UpdateRequest {
		return &testpb.UpdateRequest{
			ExtraData: newTestStruct(map[string]any{"color": "blue", "new": "x"}),
			StructMap: map[string]*structpb.Struct{"k": newTestStruct(map[string]any{"color": "blue"})},
		}
	}

	tests := []struct {
		name      string
		paths     []string
		wantPlan  []string
		wantExtra map[string]any
		wantMap   map[string]any
	}{
		{
			name:      "keys replace or delete entries",
			paths:     []string{"extraData.color", "extra_data.size", "extra_data.`a.b`", "extra_data.new", "extra_data.absent"},
			wantPlan:  []string{"extra_data.`a.b`", "extra_data.absent", "extra_data.color", "extra_data.new", "extra_data.size"},
			wantExtra: map[string]any{"color": "blue", "new": "x", "keep": true},
			wantMap:   map[string]any{"color": "red", "size": 1.0},
		},
		{
			name:      "field replaces the whole struct",
			paths:     []string{"extra_data"},
			wantPlan:  []string{"extra_data"},
			wantExtra: map[string]any{"color": "blue", "new": "x"},
			wantMap:   map[string]any{"color": "red", "size": 1.0},
		},
		{
			name:      "wildcard replaces the whole struct",
			paths:     []string{"extra_data.*"},
			wantPlan:  []string{"extra_data"},
			wantExtra: map[string]any{"color": "blue", "new": "x"},
			wantMap:   map[string]any{"color": "red", "size": 1.0},
		},
		{
			name:      "keys of a struct map entry",
			paths:     []string{"struct_map.k.color", "struct_map.k.size"},
			wantPlan:  []string{"struct_map.k.color", "struct_map.k.size"},
			wantExtra: map[string]any{"color": "red", "size": 1.0, "a.b": "dotted", "keep": true},
			wantMap:   map[string]any{"color": "blue"},
		},
	}

	spec, err := update.NewSpec(&testpb.UpdateRequest{})
	require.NoError(t, err)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			check := func(t *testing.T, got *testpb.UpdateRequest) {
				t.Helper()
				require.Equal(t, tc.wantExtra, got.GetExtraData().AsMap())
				require.Equal(t, tc.wantMap, got.GetStructMap()["k"].AsMap())
			}

			dst := stored()
			require.NoError(t, update.MergePaths(dst, request(), tc.paths))
			check(t, dst)

			dst = stored()
			require.NoError(t, update.Merge(fieldmask.FromPaths(tc.paths...), dst, request()))
			check(t, dst)

			plan, err := spec.Plan(request(), &fieldmaskpb.FieldMask{Paths: tc.paths}, update.WithCurrent(stored()))
			require.NoError(t, err)
			require.Equal(t, tc.wantPlan, plan.Paths)

			dst = stored()
			require.NoError(t, update.MergePaths(dst, request(), plan.Paths))
			check(t, dst)

			// ApplyUpdateMask cleans the request: it keeps exactly the entries
			// the mask names, the rest of the update is the service's merge.
			req := request()
			require.NoError(t, update.Apply(fieldmask.FromPaths(tc.paths...), req, update.WithCurrent(stored())))

			dst = stored()
			require.NoError(t, update.MergePaths(dst, req, plan.Paths))
			check(t, dst)
		})
	}
}

// TestStructEntryMergeCreatesOnlyWhatTheRequestHolds pins that a key path
// creates the Struct only when the request holds the entry.
func TestStructEntryMergeCreatesOnlyWhatTheRequestHolds(t *testing.T) {
	t.Parallel()

	dst := &testpb.UpdateRequest{}
	require.NoError(t, update.MergePaths(dst, &testpb.UpdateRequest{}, []string{"extra_data.color"}))
	require.Nil(t, dst.GetExtraData(), "deleting an absent entry creates no struct")

	src := &testpb.UpdateRequest{ExtraData: newTestStruct(map[string]any{"color": "blue"})}
	require.NoError(t, update.MergePaths(dst, src, []string{"extra_data.color"}))
	require.Equal(t, map[string]any{"color": "blue"}, dst.GetExtraData().AsMap())

	src.GetExtraData().GetFields()["color"] = structpb.NewStringValue("green")
	require.Equal(t, "blue", dst.GetExtraData().GetFields()["color"].GetStringValue(), "dst shares no memory with src")
}

// TestFilterAndPruneStructEntries pins that a read mask addresses Struct
// entries as map entries by key or by "*", and that strict dynamic paths
// reject a path below an entry.
func TestFilterAndPruneStructEntries(t *testing.T) {
	t.Parallel()

	msg := structRequest()
	require.NoError(t, fieldmask.FromPaths("extra_data.*").Filter(msg))
	require.True(t, proto.Equal(structRequest().GetExtraData(), msg.GetExtraData()))

	msg = structRequest()
	require.NoError(t, fieldmask.FromPaths("extra_data.`a.b`").Filter(msg))
	require.Equal(t, map[string]any{"a.b": "dotted"}, msg.GetExtraData().AsMap())

	msg = structRequest()
	require.NoError(t, fieldmask.FromPaths("extra_data.*").Prune(msg))
	require.Empty(t, msg.GetExtraData().GetFields())

	msg = structRequest()
	before := proto.Clone(msg)
	requireInvalidPath(t, fieldmask.FromPaths("extra_data.nested.inner").Filter(msg, fieldmask.WithStrictDynamicPaths(true)), "extra_data.nested.inner")
	require.True(t, proto.Equal(before, msg), "a rejected mask leaves msg untouched")
}

// TestFromSetFieldsStopsAtStructEntries pins that the paths of populated
// fields name Struct entries but nothing below them, so they are valid masks.
func TestFromSetFieldsStopsAtStructEntries(t *testing.T) {
	t.Parallel()

	msg := &testpb.UpdateRequest{
		ExtraData: newTestStruct(map[string]any{"nested": map[string]any{"inner": 1.0}}),
		StructMap: map[string]*structpb.Struct{"k": newTestStruct(map[string]any{"nested": map[string]any{"inner": 1.0}})},
		ValueData: structpb.NewStructValue(newTestStruct(map[string]any{"color": "red"})),
	}

	paths := fieldmask.FromSetFields[[]string](msg)
	require.ElementsMatch(t, []string{"extra_data", "extra_data.nested", "struct_map", "struct_map.k", "struct_map.k.nested", "value_data"}, paths)
	require.NoError(t, fieldmask.FromPaths(paths...).Validate(msg))
	require.NoError(t, update.MergePaths(&testpb.UpdateRequest{}, msg, paths))
}

// TestDeepDynamicPathsMerge pins the update semantics of a path into dynamic
// JSON below a Struct key in MergePaths, Merge, Plan and ApplyUpdateMask: a
// nested key is replaced from the request or deleted when the request lacks
// it, an intermediate Struct is created only when the request holds a value
// to write and replaces a stored value of another shape, and "*" on a list
// updates the stored elements in place by index.
func TestDeepDynamicPathsMerge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		stored    map[string]any
		request   map[string]any
		paths     []string
		wantPlan  []string
		want      map[string]any
		wantUnset bool
	}{
		{
			name:     "nested key replaced and sibling deleted",
			stored:   map[string]any{"work": map[string]any{"from": "a", "till": "b"}, "other": 1.0},
			request:  map[string]any{"work": map[string]any{"from": "x"}},
			paths:    []string{"extra_data.work.from", "extraData.work.till"},
			wantPlan: []string{"extra_data.work.from", "extra_data.work.till"},
			want:     map[string]any{"work": map[string]any{"from": "x"}, "other": 1.0},
		},
		{
			name:     "intermediate created when the request holds the value",
			stored:   map[string]any{"other": 1.0},
			request:  map[string]any{"work": map[string]any{"from": "x"}},
			paths:    []string{"extra_data.work.from"},
			wantPlan: []string{"extra_data.work.from"},
			want:     map[string]any{"other": 1.0, "work": map[string]any{"from": "x"}},
		},
		{
			name:     "no intermediate without a value to write",
			stored:   map[string]any{"other": 1.0},
			request:  map[string]any{"work": map[string]any{}},
			paths:    []string{"extra_data.work.from"},
			wantPlan: []string{"extra_data.work.from"},
			want:     map[string]any{"other": 1.0},
		},
		{
			name:      "no struct created for a deletion",
			request:   map[string]any{},
			paths:     []string{"extra_data.work.from"},
			wantPlan:  []string{"extra_data.work.from"},
			wantUnset: true,
		},
		{
			name:     "stored intermediate of another shape replaced",
			stored:   map[string]any{"work": "string"},
			request:  map[string]any{"work": map[string]any{"from": "x"}},
			paths:    []string{"extra_data.work.from"},
			wantPlan: []string{"extra_data.work.from"},
			want:     map[string]any{"work": map[string]any{"from": "x"}},
		},
		{
			name:     "request intermediate of another shape deletes the key",
			stored:   map[string]any{"work": map[string]any{"from": "a", "till": "b"}},
			request:  map[string]any{"work": "string"},
			paths:    []string{"extra_data.work.from"},
			wantPlan: []string{"extra_data.work.from"},
			want:     map[string]any{"work": map[string]any{"till": "b"}},
		},
		{
			name:     "wildcard over the keys the request holds",
			stored:   map[string]any{"a": map[string]any{"n": 1.0, "m": 1.0}, "b": map[string]any{"n": 2.0}},
			request:  map[string]any{"a": map[string]any{"n": 10.0}},
			paths:    []string{"extra_data.*.n"},
			wantPlan: []string{"extra_data.*.n"},
			want:     map[string]any{"a": map[string]any{"n": 10.0, "m": 1.0}, "b": map[string]any{"n": 2.0}},
		},
		{
			name: "list elements updated in place by index",
			stored: map[string]any{"items": []any{
				map[string]any{"name": "a", "id": 1.0}, map[string]any{"name": "b", "id": 2.0},
			}},
			request:  map[string]any{"items": []any{map[string]any{"name": "A"}, map[string]any{"name": "B"}}},
			paths:    []string{"extra_data.items.*.name"},
			wantPlan: []string{"extra_data.items.*.name"},
			want: map[string]any{"items": []any{
				map[string]any{"name": "A", "id": 1.0}, map[string]any{"name": "B", "id": 2.0},
			}},
		},
		{
			name:     "an empty request list clears the sub-path in every element",
			stored:   map[string]any{"items": []any{map[string]any{"name": "a", "id": 1.0}}},
			request:  map[string]any{"items": []any{}},
			paths:    []string{"extra_data.items.*.name"},
			wantPlan: []string{"extra_data.items.*.name"},
			want:     map[string]any{"items": []any{map[string]any{"id": 1.0}}},
		},
		{
			name:     "trailing wildcard replaces the value whole",
			stored:   map[string]any{"work": map[string]any{"from": "a"}},
			request:  map[string]any{"work": map[string]any{"till": "b"}},
			paths:    []string{"extra_data.work.*"},
			wantPlan: []string{"extra_data.work"},
			want:     map[string]any{"work": map[string]any{"till": "b"}},
		},
	}

	spec, err := update.NewSpec(&testpb.UpdateRequest{})
	require.NoError(t, err)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stored := func() *testpb.UpdateRequest {
				if tc.stored == nil {
					return &testpb.UpdateRequest{}
				}

				return &testpb.UpdateRequest{ExtraData: newTestStruct(tc.stored)}
			}
			request := func() *testpb.UpdateRequest { return &testpb.UpdateRequest{ExtraData: newTestStruct(tc.request)} }
			check := func(t *testing.T, got *testpb.UpdateRequest) {
				t.Helper()

				if tc.wantUnset {
					require.Nil(t, got.GetExtraData())
					return
				}

				require.Equal(t, tc.want, got.GetExtraData().AsMap())
			}

			dst := stored()
			require.NoError(t, update.MergePaths(dst, request(), tc.paths))
			check(t, dst)

			dst = stored()
			require.NoError(t, update.Merge(fieldmask.FromPaths(tc.paths...), dst, request()))
			check(t, dst)

			plan, err := spec.Plan(request(), &fieldmaskpb.FieldMask{Paths: tc.paths}, update.WithCurrent(stored()))
			require.NoError(t, err)
			require.Equal(t, tc.wantPlan, plan.Paths)

			dst = stored()
			require.NoError(t, update.MergePaths(dst, request(), plan.Paths))
			check(t, dst)

			req := request()
			require.NoError(t, update.Apply(fieldmask.FromPaths(tc.paths...), req, update.WithCurrent(stored())))

			dst = stored()
			require.NoError(t, update.MergePaths(dst, req, plan.Paths))
			check(t, dst)
		})
	}
}

// TestDeepDynamicListCountMismatch pins that "*" on a list value refuses a
// request holding another number of elements than the stored list, before
// any write, with the structured errors of a repeated field.
func TestDeepDynamicListCountMismatch(t *testing.T) {
	t.Parallel()

	stored := &testpb.UpdateRequest{
		ExtraData: newTestStruct(map[string]any{"items": []any{map[string]any{"name": "a"}, map[string]any{"name": "b"}}}),
		ListData:  newTestListValue(map[string]any{"name": "a"}),
	}

	for _, tc := range []struct {
		path    string
		request *testpb.UpdateRequest
	}{
		{path: "extra_data.items.*.name", request: &testpb.UpdateRequest{
			ExtraData: newTestStruct(map[string]any{"items": []any{map[string]any{"name": "A"}}}),
		}},
		{path: "list_data.*.name", request: &testpb.UpdateRequest{
			ListData: newTestListValue(map[string]any{"name": "A"}, map[string]any{"name": "B"}),
		}},
	} {
		dst := proto.Clone(stored)
		err := update.MergePaths(dst, tc.request, []string{tc.path})
		require.ErrorIs(t, err, update.ErrElementCountMismatch, tc.path)
		require.True(t, proto.Equal(stored, dst), "nothing is written: %s", tc.path)

		spec, err := update.NewSpec(&testpb.UpdateRequest{})
		require.NoError(t, err)

		_, err = spec.Plan(tc.request, &fieldmaskpb.FieldMask{Paths: []string{"description", tc.path}}, update.WithCurrent(stored))
		require.ErrorIs(t, err, update.ErrElementCountMismatch, tc.path)

		mp, ok := errors.AsType[*update.MaskPathError](err)
		require.True(t, ok, tc.path)
		require.Equal(t, 1, mp.Index, tc.path)
	}
}

// TestDeepDynamicValueAndListFields pins paths into a google.protobuf.Value
// field holding a Struct and through "*" of a ListValue field.
func TestDeepDynamicValueAndListFields(t *testing.T) {
	t.Parallel()

	dst := &testpb.UpdateRequest{ValueData: structpb.NewStringValue("scalar")}
	src := &testpb.UpdateRequest{ValueData: structpb.NewStructValue(newTestStruct(map[string]any{"key": 1.0, "other": 2.0}))}
	require.NoError(t, update.MergePaths(dst, src, []string{"value_data.key"}))
	require.Equal(t, map[string]any{"key": 1.0}, dst.GetValueData().AsInterface())

	dst = &testpb.UpdateRequest{ListData: newTestListValue(map[string]any{"name": "a", "id": 1.0})}
	src = &testpb.UpdateRequest{ListData: newTestListValue(map[string]any{"name": "A"})}
	require.NoError(t, update.MergePaths(dst, src, []string{"list_data.*.name"}))
	require.Equal(t, []any{map[string]any{"name": "A", "id": 1.0}}, dst.GetListData().AsSlice())
}

// TestDeepDynamicPathsRead pins Filter and Prune over dynamic JSON: exactly
// the addressed nested key is kept or removed and "*" addresses every entry
// or element. A path through a value of another shape (a scalar or null, a
// list without "*", an element of another shape under "*") addresses
// nothing: Filter drops that value unless another path keeps it, keeping the
// other list elements in order, and Prune leaves it as it is.
func TestDeepDynamicPathsRead(t *testing.T) {
	t.Parallel()

	extra := map[string]any{
		"work":  map[string]any{"from": "a", "till": "b"},
		"items": []any{map[string]any{"name": "A", "score": 1.0}, map[string]any{"name": "B", "score": 2.0}},
		"title": "t",
	}
	mixed := map[string]any{
		"items": []any{
			map[string]any{"name": "A", "score": 1.0}, "s", 2.0, nil, []any{"x"},
			map[string]any{"score": 3.0}, map[string]any{"name": "B"},
		},
	}
	scalars := map[string]any{"title": "t", "num": 1.0, "flag": true, "none": nil, "obj": map[string]any{"x": 1.0, "y": 2.0}}

	tests := []struct {
		name      string
		paths     []string
		prune     bool
		extra     map[string]any
		value     *structpb.Value
		wantExtra map[string]any
		wantValue any
		noValue   bool
		wantList  []any
	}{
		{
			name: "filter a nested key", paths: []string{"extra_data.work.from"},
			wantExtra: map[string]any{"work": map[string]any{"from": "a"}},
		},
		{
			name: "prune a nested key", paths: []string{"extra_data.work.from"}, prune: true,
			wantExtra: map[string]any{
				"work":  map[string]any{"till": "b"},
				"items": []any{map[string]any{"name": "A", "score": 1.0}, map[string]any{"name": "B", "score": 2.0}},
				"title": "t",
			},
		},
		{
			name: "filter keeps a struct without the addressed key", paths: []string{"extra_data.work.absent"},
			wantExtra: map[string]any{"work": map[string]any{}},
		},
		{
			name: "filter list elements through *", paths: []string{"extra_data.items.*.name"},
			wantExtra: map[string]any{"items": []any{map[string]any{"name": "A"}, map[string]any{"name": "B"}}},
		},
		{
			name: "prune list elements through *", paths: []string{"extra_data.items.*.score"}, prune: true,
			wantExtra: map[string]any{
				"work":  map[string]any{"from": "a", "till": "b"},
				"items": []any{map[string]any{"name": "A"}, map[string]any{"name": "B"}},
				"title": "t",
			},
		},
		{
			name: "prune every element", paths: []string{"extra_data.items.*"}, prune: true,
			wantExtra: map[string]any{"work": map[string]any{"from": "a", "till": "b"}, "items": []any{}, "title": "t"},
		},
		{
			name: "filter every key's nested key drops values of another shape", paths: []string{"extra_data.*.from"},
			wantExtra: map[string]any{"work": map[string]any{"from": "a"}},
		},
		{
			name: "prune every key's nested key leaves values of another shape", paths: []string{"extra_data.*.from"}, prune: true,
			wantExtra: map[string]any{
				"work":  map[string]any{"till": "b"},
				"items": []any{map[string]any{"name": "A", "score": 1.0}, map[string]any{"name": "B", "score": 2.0}},
				"title": "t",
			},
		},
		{
			name: "filter drops a list without *", paths: []string{"extra_data.items.name"},
			wantExtra: map[string]any{},
		},
		{
			name: "prune leaves a list without *", paths: []string{"extra_data.items.name"}, prune: true,
			wantExtra: extra,
		},
		{
			name: "filter drops a scalar", paths: []string{"extra_data.title.x"},
			wantExtra: map[string]any{},
		},
		{
			name: "prune leaves a scalar", paths: []string{"extra_data.title.x"}, prune: true,
			wantExtra: extra,
		},
		{
			name:  "filter drops strings, numbers, bools and nulls under a key",
			paths: []string{"extra_data.title.x", "extra_data.num.x", "extra_data.flag.x", "extra_data.none.x", "extra_data.obj.x"},
			extra: scalars, wantExtra: map[string]any{"obj": map[string]any{"x": 1.0}},
		},
		{
			name:  "prune leaves strings, numbers, bools and nulls under a key",
			paths: []string{"extra_data.title.x", "extra_data.num.x", "extra_data.flag.x", "extra_data.none.x", "extra_data.obj.x"},
			prune: true, extra: scalars,
			wantExtra: map[string]any{"title": "t", "num": 1.0, "flag": true, "none": nil, "obj": map[string]any{"y": 2.0}},
		},
		{
			name: "filter drops the elements of another shape under *", paths: []string{"extra_data.items.*.name"}, extra: mixed,
			wantExtra: map[string]any{"items": []any{map[string]any{"name": "A"}, map[string]any{}, map[string]any{"name": "B"}}},
		},
		{
			name: "prune leaves the elements of another shape under *", paths: []string{"extra_data.items.*.name"}, prune: true, extra: mixed,
			wantExtra: map[string]any{"items": []any{
				map[string]any{"score": 1.0}, "s", 2.0, nil, []any{"x"}, map[string]any{"score": 3.0}, map[string]any{},
			}},
		},
		{
			name: "filter drops the scalar elements of a struct's keys under *", paths: []string{"extra_data.*.*.x"},
			extra:     map[string]any{"obj": map[string]any{"a": map[string]any{"x": 1.0, "y": 2.0}, "b": "s"}},
			wantExtra: map[string]any{"obj": map[string]any{"a": map[string]any{"x": 1.0}}},
		},
		{
			name: "another path keeps the scalar", paths: []string{"extra_data.title.x", "extra_data.title"},
			wantExtra: map[string]any{"title": "t"},
		},
		{
			name: "another path keeps the list without *", paths: []string{"extra_data.items.name", "extra_data.items.*.score"},
			wantExtra: map[string]any{"items": []any{map[string]any{"score": 1.0}, map[string]any{"score": 2.0}}},
		},
		{
			name: "filter a key of a Value field", paths: []string{"value_data.key"},
			wantValue: map[string]any{"key": "v"},
		},
		{
			name: "prune a key of a Value field", paths: []string{"value_data.key"}, prune: true,
			wantValue: map[string]any{"num": 1.0},
		},
		{
			name: "filter clears a scalar Value field", paths: []string{"value_data.key"}, value: structpb.NewStringValue("s"),
			noValue: true,
		},
		{
			name: "filter clears a list Value field without *", paths: []string{"value_data.key"},
			value: structpb.NewListValue(newTestListValue("s")), noValue: true,
		},
		{
			name: "prune leaves a scalar Value field", paths: []string{"value_data.key"}, prune: true, value: structpb.NewStringValue("s"),
			wantValue: "s",
		},
		{
			name: "filter the elements of a ListValue field", paths: []string{"list_data.*.name"},
			wantList: []any{map[string]any{"name": "A"}},
		},
		{
			name: "prune the elements of a ListValue field", paths: []string{"list_data.*.name"}, prune: true,
			wantList: []any{map[string]any{"age": 1.0}, "scalar"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			msg := &testpb.UpdateRequest{
				Id:        "1",
				ExtraData: newTestStruct(extra),
				ValueData: structpb.NewStructValue(newTestStruct(map[string]any{"key": "v", "num": 1.0})),
				ListData:  newTestListValue(map[string]any{"name": "A", "age": 1.0}, "scalar"),
			}
			if tc.extra != nil {
				msg.ExtraData = newTestStruct(tc.extra)
			}

			if tc.value != nil {
				msg.ValueData = tc.value
			}

			mask := fieldmask.FromPaths(tc.paths...)

			if tc.prune {
				require.NoError(t, mask.Prune(msg))
			} else {
				require.NoError(t, mask.Filter(msg))
			}

			if tc.wantExtra != nil {
				require.Equal(t, tc.wantExtra, msg.GetExtraData().AsMap())
			}

			if tc.wantValue != nil {
				require.Equal(t, tc.wantValue, msg.GetValueData().AsInterface())
			}

			if tc.noValue {
				require.Nil(t, msg.GetValueData())
			}

			if tc.wantList != nil {
				require.Equal(t, tc.wantList, msg.GetListData().AsSlice())
			}

			_, err := fieldmask.FromPaths(tc.paths[0]).Resolve(msg.ProtoReflect().Descriptor(), fieldmask.WithStrictDynamicPaths(true))
			requireInvalidPath(t, err, tc.paths[0])
		})
	}
}

// TestDeepDynamicPathsSelect pins that Select keeps what Filter keeps in
// dynamic JSON, dropping a value the mask addresses nothing of.
func TestDeepDynamicPathsSelect(t *testing.T) {
	t.Parallel()

	msg := &testpb.UpdateRequest{
		ExtraData: newTestStruct(map[string]any{"title": "t", "work": map[string]any{"from": "a", "till": "b"}}),
		ValueData: structpb.NewNullValue(),
	}

	require.NoError(t, fieldmask.FromPaths("extra_data.title.x", "extra_data.work.from", "value_data.x").Select(msg))
	require.Equal(t, map[string]any{"work": map[string]any{"from": "a"}}, msg.GetExtraData().AsMap())
	require.Nil(t, msg.GetValueData())
}

// structRequest returns an UpdateRequest holding every dynamic field.
func structRequest() *testpb.UpdateRequest {
	return &testpb.UpdateRequest{
		Id:         "1",
		ExtraData:  newTestStruct(map[string]any{"color": "red", "a.b": "dotted", "nested": map[string]any{"inner": 1.0}}),
		StructMap:  map[string]*structpb.Struct{"k": newTestStruct(map[string]any{"color": "red"})},
		StructList: []*structpb.Struct{newTestStruct(map[string]any{"color": "red"})},
		ValueData:  structpb.NewStringValue("v"),
		ListData:   newTestListValue("a", 1.0),
	}
}

// requireAcceptedOrInvalid requires err to be nil when accepted, else a
// *ValidationError matching ErrInvalidPath.
func requireAcceptedOrInvalid(t *testing.T, accepted bool, err error, entry string) {
	t.Helper()

	if accepted {
		require.NoError(t, err, entry)

		return
	}

	var ve *fieldmask.ValidationError
	require.ErrorAs(t, err, &ve, entry)
	require.ErrorIs(t, err, fieldmask.ErrInvalidPath, entry)
}

// requirePlanPathError requires err to be the Plan refusal of the mask path
// raw at index: a FieldError on updateMask.paths[index] wrapping a
// *MaskPathError that matches ErrInvalidPath.
func requirePlanPathError(t *testing.T, err error, index int, raw string) {
	t.Helper()

	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok, "want a FieldError, got %v", err)
	require.Equal(t, update.DefaultUpdateMaskField+".paths[1]", fe.Field)

	mp, ok := errors.AsType[*update.MaskPathError](err)
	require.True(t, ok, "want a MaskPathError, got %v", err)
	require.Equal(t, index, mp.Index)
	require.Equal(t, raw, mp.Path)
	require.ErrorIs(t, err, fieldmask.ErrInvalidPath)
}
