// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldmask_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	testpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
)

// --- helpers ---

func newTestStruct(kvs map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(kvs)
	if err != nil {
		panic(err)
	}

	return s
}

func msgWithExtraData() *testpb.UpdateRequest {
	return &testpb.UpdateRequest{
		Id:        "1",
		ExtraData: newTestStruct(map[string]any{"key": "val", "num": 42.0}),
	}
}

// --- Filter ---

func TestFilter_StructAsLeaf_KeptIntact(t *testing.T) {
	msg := msgWithExtraData()
	original := proto.Clone(msg).(*testpb.UpdateRequest)

	mask := fieldmask.FromPaths("extra_data")
	mask.Filter(msg)

	assert.True(t, proto.Equal(original.GetExtraData(), msg.GetExtraData()),
		"Struct value should be kept intact when entire field is in mask")
	assert.Equal(t, "", msg.GetId(), "id should be cleared (not in mask)")
}

func TestFilter_StructNestedMask_FiltersKeys(t *testing.T) {
	msg := msgWithExtraData() // {"key": "val", "num": 42}

	mask := fieldmask.FromPaths("extra_data.key")
	mask.Filter(msg)

	// Only "key" should remain; "num" should be removed.
	require.NotNil(t, msg.GetExtraData())
	assert.NotNil(t, msg.GetExtraData().GetFields()["key"], "key should be kept")
	assert.Nil(t, msg.GetExtraData().GetFields()["num"], "num should be filtered out")
}

func TestFilter_StructNotInMask_Cleared(t *testing.T) {
	msg := msgWithExtraData()

	mask := fieldmask.FromPaths("id")
	mask.Filter(msg)

	assert.Equal(t, "1", msg.GetId())
	assert.Nil(t, msg.GetExtraData(), "extra_data should be cleared when not in mask")
}

// --- Prune ---

func TestPrune_StructAsLeaf_Cleared(t *testing.T) {
	msg := msgWithExtraData()

	mask := fieldmask.FromPaths("extra_data")
	mask.Prune(msg)

	assert.Nil(t, msg.GetExtraData(), "extra_data should be pruned")
	assert.Equal(t, "1", msg.GetId(), "id should remain")
}

func TestPrune_StructNestedMask_PrunesKey(t *testing.T) {
	msg := msgWithExtraData() // {"key": "val", "num": 42}

	mask := fieldmask.FromPaths("extra_data.key")
	mask.Prune(msg)

	require.NotNil(t, msg.GetExtraData())
	assert.Nil(t, msg.GetExtraData().GetFields()["key"], "key should be pruned")
	assert.NotNil(t, msg.GetExtraData().GetFields()["num"], "num should remain")
}

func TestPrune_StructNestedMask_NonExistentKey_NoOp(t *testing.T) {
	msg := msgWithExtraData()
	original := proto.Clone(msg).(*testpb.UpdateRequest)

	// Pruning a key that doesn't exist in the Struct is a no-op.
	mask := fieldmask.FromPaths("extra_data.nonexistent")
	mask.Prune(msg)

	assert.True(t, proto.Equal(original.GetExtraData(), msg.GetExtraData()),
		"Struct should be unchanged when pruning nonexistent key")
}

// --- FromSetFields ---

func TestFromSetFields_StructEnumeratesKeys(t *testing.T) {
	msg := msgWithExtraData() // {"key": "val", "num": 42}

	paths := fieldmask.FromSetFields[[]string](msg)
	slices.Sort(paths)

	assert.Contains(t, paths, "extra_data")
	assert.Contains(t, paths, "extra_data.key", "Struct keys should appear as sub-paths")
	assert.Contains(t, paths, "extra_data.num", "Struct keys should appear as sub-paths")
	// Internal proto fields like "extra_data.fields" must NOT appear.
	for _, p := range paths {
		assert.NotContains(t, p, "extra_data.fields",
			"internal proto structure should not leak into paths")
	}
}

func TestFromMessage_StructEnumeratesKeys(t *testing.T) {
	msg := msgWithExtraData()

	paths := fieldmask.FromMessage[[]string](msg)
	slices.Sort(paths)

	assert.Contains(t, paths, "extra_data")
	assert.Contains(t, paths, "extra_data.key")
	assert.Contains(t, paths, "extra_data.num")

	for _, p := range paths {
		assert.NotContains(t, p, "extra_data.fields",
			"internal proto structure should not leak into paths")
	}
}

// --- Validate ---

func TestValidate_StructLeafPath_Valid(t *testing.T) {
	msg := &testpb.UpdateRequest{}
	mask := fieldmask.FromPaths("extra_data")
	assert.NoError(t, mask.Validate(msg))
}

// --- ApplyUpdateMask ---

func TestApplyUpdateMask_StructInMask_Kept(t *testing.T) {
	name := "John"
	msg := &testpb.UpdateRequest{
		Id:        "1",
		Name:      &name,
		ExtraData: newTestStruct(map[string]any{"a": "b"}),
	}
	original := proto.Clone(msg.GetExtraData()).(*structpb.Struct)

	mask := fieldmask.FromPaths("name", "extra_data")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	assert.True(t, proto.Equal(original, msg.GetExtraData()),
		"extra_data should be kept when in mask")
	assert.Equal(t, "John", msg.GetName())
	assert.Equal(t, "1", msg.GetId(), "id addresses the resource and is kept")
}

func TestApplyUpdateMask_StructNotSet_StaysUnset(t *testing.T) {
	name := "John"
	msg := &testpb.UpdateRequest{
		Id:   "1",
		Name: &name,
	}

	mask := fieldmask.FromPaths("name", "extra_data")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	// extra_data was not set but is in the mask — it stays unset, so the update clears it.
	require.Nil(t, msg.GetExtraData(), "no empty Struct is created")
}

// --- Filter with readMask (user's use-case) ---

func TestFilter_StructReadMask_WorkPeriod(t *testing.T) {
	// Simulates the user's exact use-case from the bug report.
	msg := &testpb.UpdateRequest{
		Id: "cf691bdd",
		ExtraData: newTestStruct(map[string]any{
			"type": "PIECEWORK",
			"workPeriod": map[string]any{
				"from": "2026-02-18",
				"till": "2026-02-19",
			},
			"amount":      352500.0,
			"displayName": "Forklift driver for a food warehouse",
		}),
	}

	mask := fieldmask.FromPaths("extra_data.workPeriod")
	mask.Filter(msg)

	require.NotNil(t, msg.GetExtraData())
	fields := msg.GetExtraData().GetFields()

	// Only workPeriod should remain.
	assert.Len(t, fields, 1)
	wp := fields["workPeriod"]
	require.NotNil(t, wp)
	require.NotNil(t, wp.GetStructValue())
	assert.Equal(t, "2026-02-18", wp.GetStructValue().GetFields()["from"].GetStringValue())
	assert.Equal(t, "2026-02-19", wp.GetStructValue().GetFields()["till"].GetStringValue())
}

// --- Map of Struct ---

func TestFilter_MapOfStruct_KeysFilterNormally(t *testing.T) {
	msg := &testpb.UpdateRequest{
		Id: "1",
		StructMap: map[string]*structpb.Struct{
			"keep": newTestStruct(map[string]any{"x": 1.0}),
			"drop": newTestStruct(map[string]any{"y": 2.0}),
		},
	}

	mask := fieldmask.FromPaths("struct_map.keep")
	mask.Filter(msg)

	require.Len(t, msg.GetStructMap(), 1)
	assert.NotNil(t, msg.GetStructMap()["keep"])
	assert.Nil(t, msg.GetStructMap()["drop"])
}

func TestFilter_MapOfStruct_ValueFiltered(t *testing.T) {
	msg := &testpb.UpdateRequest{
		Id: "1",
		StructMap: map[string]*structpb.Struct{
			"config": newTestStruct(map[string]any{"a": 1.0, "b": 2.0}),
		},
	}

	// Keep only key "a" inside the Struct at map entry "config".
	mask := fieldmask.FromPaths("struct_map.config.a")
	mask.Filter(msg)

	require.Len(t, msg.GetStructMap(), 1)
	s := msg.GetStructMap()["config"]
	require.NotNil(t, s)
	assert.NotNil(t, s.GetFields()["a"], "a should be kept")
	assert.Nil(t, s.GetFields()["b"], "b should be filtered out")
}

func TestPrune_MapOfStruct_KeysPruneNormally(t *testing.T) {
	msg := &testpb.UpdateRequest{
		Id: "1",
		StructMap: map[string]*structpb.Struct{
			"keep": newTestStruct(map[string]any{"x": 1.0}),
			"drop": newTestStruct(map[string]any{"y": 2.0}),
		},
	}

	mask := fieldmask.FromPaths("struct_map.drop")
	mask.Prune(msg)

	require.Len(t, msg.GetStructMap(), 1)
	assert.NotNil(t, msg.GetStructMap()["keep"])
}

func TestFromSetFields_MapOfStruct_EnumeratesKeys(t *testing.T) {
	msg := &testpb.UpdateRequest{
		StructMap: map[string]*structpb.Struct{
			"a": newTestStruct(map[string]any{"k": "v"}),
		},
	}

	paths := fieldmask.FromSetFields[[]string](msg)
	slices.Sort(paths)

	assert.Contains(t, paths, "struct_map")
	assert.Contains(t, paths, "struct_map.a")
	assert.Contains(t, paths, "struct_map.a.k", "Struct keys inside map values should be enumerated")
	// Must NOT recurse into Struct's internal proto fields.
	for _, p := range paths {
		assert.NotContains(t, p, "struct_map.a.fields",
			"should not recurse into internal proto structure")
	}
}

// --- Repeated Struct ---

func TestFilter_RepeatedStruct_KeptAsIs(t *testing.T) {
	msg := &testpb.UpdateRequest{
		Id: "1",
		StructList: []*structpb.Struct{
			newTestStruct(map[string]any{"a": 1.0}),
			newTestStruct(map[string]any{"b": 2.0}),
		},
	}

	mask := fieldmask.FromPaths("struct_list")
	mask.Filter(msg)

	require.Len(t, msg.GetStructList(), 2)
}

func TestPrune_RepeatedStruct_Cleared(t *testing.T) {
	msg := &testpb.UpdateRequest{
		Id: "1",
		StructList: []*structpb.Struct{
			newTestStruct(map[string]any{"a": 1.0}),
		},
	}

	mask := fieldmask.FromPaths("struct_list")
	mask.Prune(msg)

	assert.Empty(t, msg.GetStructList())
	assert.Equal(t, "1", msg.GetId())
}

func TestFromSetFields_RepeatedStruct_EnumeratesKeys(t *testing.T) {
	msg := &testpb.UpdateRequest{
		StructList: []*structpb.Struct{
			newTestStruct(map[string]any{"k": "v"}),
		},
	}

	paths := fieldmask.FromSetFields[[]string](msg)

	// A repeated field is a leaf: elements are never addressed by index.
	assert.Equal(t, []string{"struct_list"}, paths)
	// Must NOT recurse into Struct's internal proto fields.
	for _, p := range paths {
		assert.NotContains(t, p, "struct_list.0.fields",
			"should not recurse into internal proto structure")
	}
}

func TestValidate_MapOfStruct_Valid(t *testing.T) {
	msg := &testpb.UpdateRequest{}
	mask := fieldmask.FromPaths("struct_map.any_key")
	assert.NoError(t, mask.Validate(msg))
}

func TestValidate_RepeatedStruct_Valid(t *testing.T) {
	msg := &testpb.UpdateRequest{}
	mask := fieldmask.FromPaths("struct_list")
	assert.NoError(t, mask.Validate(msg))
}

// --- ListValue helpers ---

func newTestListValue(items ...any) *structpb.ListValue {
	lv, err := structpb.NewList(items)
	if err != nil {
		panic(err)
	}

	return lv
}

func msgWithListData() *testpb.UpdateRequest {
	return &testpb.UpdateRequest{
		Id: "1",
		ListData: newTestListValue(
			map[string]any{"name": "Alice", "age": 30.0},
			map[string]any{"name": "Bob", "age": 25.0},
		),
	}
}

// --- ListValue Filter ---

func TestFilter_ListValueAsLeaf_KeptIntact(t *testing.T) {
	msg := msgWithListData()
	original := proto.Clone(msg).(*testpb.UpdateRequest)

	mask := fieldmask.FromPaths("list_data")
	mask.Filter(msg)

	assert.True(t, proto.Equal(original.GetListData(), msg.GetListData()),
		"ListValue should be kept intact when entire field is in mask")
	assert.Equal(t, "", msg.GetId(), "id should be cleared (not in mask)")
}

func TestFilter_ListValueNotInMask_Cleared(t *testing.T) {
	msg := msgWithListData()

	mask := fieldmask.FromPaths("id")
	mask.Filter(msg)

	assert.Equal(t, "1", msg.GetId())
	assert.Nil(t, msg.GetListData(), "list_data should be cleared when not in mask")
}

// --- ListValue Prune ---

func TestPrune_ListValueAsLeaf_Cleared(t *testing.T) {
	msg := msgWithListData()

	mask := fieldmask.FromPaths("list_data")
	mask.Prune(msg)

	assert.Nil(t, msg.GetListData(), "list_data should be pruned")
	assert.Equal(t, "1", msg.GetId(), "id should remain")
}

// --- ListValue FromSetFields ---

func TestFromSetFields_ListValueEnumeratesElements(t *testing.T) {
	msg := &testpb.UpdateRequest{
		ListData: newTestListValue(
			map[string]any{"k": "v"},
		),
	}

	paths := fieldmask.FromSetFields[[]string](msg)

	// A ListValue is a leaf: elements are never addressed by index.
	assert.Equal(t, []string{"list_data"}, paths)
	// Must NOT recurse into ListValue's internal proto fields.
	for _, p := range paths {
		assert.NotContains(t, p, "list_data.values",
			"internal proto structure should not leak into paths")
	}
}

// --- ListValue Validate ---

func TestValidate_ListValueLeafPath_Valid(t *testing.T) {
	msg := &testpb.UpdateRequest{}
	mask := fieldmask.FromPaths("list_data")
	assert.NoError(t, mask.Validate(msg))
}

// --- ApplyUpdateMask with ListValue ---

func TestApplyUpdateMask_ListValueInMask_Kept(t *testing.T) {
	name := "John"
	msg := &testpb.UpdateRequest{
		Id:       "1",
		Name:     &name,
		ListData: newTestListValue(map[string]any{"a": "b"}),
	}
	original := proto.Clone(msg.GetListData()).(*structpb.ListValue)

	mask := fieldmask.FromPaths("name", "list_data")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	assert.True(t, proto.Equal(original, msg.GetListData()),
		"list_data should be kept when in mask")
	assert.Equal(t, "1", msg.GetId(), "id addresses the resource and is kept")
}

func TestApplyUpdateMask_ListValueNotSet_StaysUnset(t *testing.T) {
	name := "John"
	msg := &testpb.UpdateRequest{
		Id:   "1",
		Name: &name,
	}

	mask := fieldmask.FromPaths("name", "list_data")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	// list_data was not set but is in the mask — it stays unset, so the update clears it.
	require.Nil(t, msg.GetListData(), "no empty ListValue is created")
}

// --- FromSetFields with nested ListValue ---

func TestFromSetFields_StructWithListValue(t *testing.T) {
	msg := &testpb.UpdateRequest{
		ExtraData: newTestStruct(map[string]any{
			"items": []any{
				map[string]any{"x": 1.0},
			},
		}),
	}

	paths := fieldmask.FromSetFields[[]string](msg)

	assert.Contains(t, paths, "extra_data")
	assert.Contains(t, paths, "extra_data.items")
	assert.NotContains(t, paths, "extra_data.items.0", "a ListValue is a leaf")
}

// --- Value helpers ---

func newTestValue(v any) *structpb.Value {
	val, err := structpb.NewValue(v)
	if err != nil {
		panic(err)
	}

	return val
}

func msgWithValueData() *testpb.UpdateRequest {
	return &testpb.UpdateRequest{
		Id:        "1",
		ValueData: newTestValue(map[string]any{"key": "val", "num": 42.0}),
	}
}

// --- Value Filter ---

func TestFilter_ValueAsLeaf_KeptIntact(t *testing.T) {
	msg := msgWithValueData()
	original := proto.Clone(msg).(*testpb.UpdateRequest)

	mask := fieldmask.FromPaths("value_data")
	mask.Filter(msg)

	assert.True(t, proto.Equal(original.GetValueData(), msg.GetValueData()),
		"Value should be kept intact when entire field is in mask")
	assert.Equal(t, "", msg.GetId(), "id should be cleared (not in mask)")
}

func TestFilter_ValueNotInMask_Cleared(t *testing.T) {
	msg := msgWithValueData()

	mask := fieldmask.FromPaths("id")
	mask.Filter(msg)

	assert.Equal(t, "1", msg.GetId())
	assert.Nil(t, msg.GetValueData(), "value_data should be cleared when not in mask")
}

// --- Value Prune ---

func TestPrune_ValueAsLeaf_Cleared(t *testing.T) {
	msg := msgWithValueData()

	mask := fieldmask.FromPaths("value_data")
	mask.Prune(msg)

	assert.Nil(t, msg.GetValueData(), "value_data should be pruned")
	assert.Equal(t, "1", msg.GetId(), "id should remain")
}

// --- Value FromSetFields ---

func TestFromSetFields_ValueWithScalar_OnlyLeafPath(t *testing.T) {
	msg := &testpb.UpdateRequest{
		ValueData: newTestValue("hello"),
	}

	paths := fieldmask.FromSetFields[[]string](msg)

	assert.Contains(t, paths, "value_data")
	// Scalar Value should produce only the leaf path, no sub-paths.
	for _, p := range paths {
		if p == "value_data" {
			continue
		}

		assert.False(t, len(p) > len("value_data") && p[:len("value_data")+1] == "value_data.",
			"scalar Value should not produce sub-paths, got: %s", p)
	}
}

// --- Value Validate ---

func TestValidate_ValueLeafPath_Valid(t *testing.T) {
	msg := &testpb.UpdateRequest{}
	mask := fieldmask.FromPaths("value_data")
	assert.NoError(t, mask.Validate(msg))
}

// --- Value ApplyUpdateMask ---

func TestApplyUpdateMask_ValueInMask_Kept(t *testing.T) {
	name := "John"
	msg := &testpb.UpdateRequest{
		Id:        "1",
		Name:      &name,
		ValueData: newTestValue(map[string]any{"a": "b"}),
	}
	original := proto.Clone(msg.GetValueData()).(*structpb.Value)

	mask := fieldmask.FromPaths("name", "value_data")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	assert.True(t, proto.Equal(original, msg.GetValueData()),
		"value_data should be kept when in mask")
	assert.Equal(t, "1", msg.GetId(), "id addresses the resource and is kept")
}

func TestApplyUpdateMask_ValueNotSet_StaysUnset(t *testing.T) {
	name := "John"
	msg := &testpb.UpdateRequest{
		Id:   "1",
		Name: &name,
	}

	mask := fieldmask.FromPaths("name", "value_data")
	err := update.Apply(mask, msg)
	require.NoError(t, err)

	// value_data was not set but is in the mask — it stays unset, so the update clears it.
	require.Nil(t, msg.GetValueData(), "no empty Value is created")
}

func TestUpdateDynamicStructPath(t *testing.T) {
	t.Parallel()

	msg := msgWithExtraData()
	mask := fieldmask.FromPaths("extra_data.key")
	_, err := update.ValidateStrict(mask, msg)
	require.NoError(t, err)
	require.NoError(t, update.Apply(mask, msg))
	require.Equal(t, map[string]any{"key": "val"}, msg.ExtraData.AsMap())
}
