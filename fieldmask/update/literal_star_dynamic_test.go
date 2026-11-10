// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	testpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
)

// dynamicRequest returns an UpdateRequest holding extra_data and value_data
// built from the JSON-like maps given (nil leaves a field unset).
func dynamicRequest(t *testing.T, extra, value map[string]any) *testpb.UpdateRequest {
	t.Helper()

	req := &testpb.UpdateRequest{}

	if extra != nil {
		s, err := structpb.NewStruct(extra)
		require.NoError(t, err)

		req.ExtraData = s
	}

	if value != nil {
		v, err := structpb.NewValue(value)
		require.NoError(t, err)

		req.ValueData = v
	}

	return req
}

// TestLiteralStarKeyInDynamicJSON pins that an entry of dynamic JSON whose
// literal key is "*" is reached through the wildcard by the merge and its
// count check as by the filter: the exact Struct case ["extra_data.*.name"],
// a google.protobuf.Value, nested JSON, and a list below such an entry. The
// effective mask of ApplyMask and Plan.Paths, merged with MergePaths, write
// the new value.
func TestLiteralStarKeyInDynamicJSON(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name              string
		path              string
		stored, req, want func(t *testing.T) *testpb.UpdateRequest
	}{
		{
			name: "Struct",
			path: "extra_data.*.name",
			stored: func(t *testing.T) *testpb.UpdateRequest {
				return dynamicRequest(t, map[string]any{"*": map[string]any{"name": "old"}}, nil)
			},
			req: func(t *testing.T) *testpb.UpdateRequest {
				return dynamicRequest(t, map[string]any{"*": map[string]any{"name": "new"}}, nil)
			},
			want: func(t *testing.T) *testpb.UpdateRequest {
				return dynamicRequest(t, map[string]any{"*": map[string]any{"name": "new"}}, nil)
			},
		},
		{
			name: "Value",
			path: "value_data.*.name",
			stored: func(t *testing.T) *testpb.UpdateRequest {
				return dynamicRequest(t, nil, map[string]any{"*": map[string]any{"name": "old"}})
			},
			req: func(t *testing.T) *testpb.UpdateRequest {
				return dynamicRequest(t, nil, map[string]any{"*": map[string]any{"name": "new"}})
			},
			want: func(t *testing.T) *testpb.UpdateRequest {
				return dynamicRequest(t, nil, map[string]any{"*": map[string]any{"name": "new"}})
			},
		},
		{
			name: "nested",
			path: "extra_data.a.*.name",
			stored: func(t *testing.T) *testpb.UpdateRequest {
				return dynamicRequest(t, map[string]any{"a": map[string]any{"*": map[string]any{"name": "old"}}}, nil)
			},
			req: func(t *testing.T) *testpb.UpdateRequest {
				return dynamicRequest(t, map[string]any{"a": map[string]any{"*": map[string]any{"name": "new"}}}, nil)
			},
			want: func(t *testing.T) *testpb.UpdateRequest {
				return dynamicRequest(t, map[string]any{"a": map[string]any{"*": map[string]any{"name": "new"}}}, nil)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{tc.path}}, tc.req(t), update.WithCurrent(tc.stored(t)))
			require.NoError(t, err)

			merged := tc.stored(t)
			require.NoError(t, update.MergePaths(merged, tc.req(t), got.ToPaths()))
			requireProtoEqual(t, tc.want(t), merged)

			merged = tc.stored(t)
			require.NoError(t, update.Merge(fieldmask.FromPaths(tc.path), merged, tc.req(t)))
			requireProtoEqual(t, tc.want(t), merged)

			spec, err := update.NewSpec(&testpb.UpdateRequest{})
			require.NoError(t, err)

			plan, err := spec.Plan(tc.req(t), &fieldmaskpb.FieldMask{Paths: []string{tc.path}}, update.WithCurrent(tc.stored(t)))
			require.NoError(t, err)

			merged = tc.stored(t)
			require.NoError(t, update.MergePaths(merged, tc.req(t), plan.Paths))
			requireProtoEqual(t, tc.want(t), merged)
		})
	}

	t.Run("count below the entry", func(t *testing.T) {
		t.Parallel()

		stored := dynamicRequest(t, map[string]any{"*": map[string]any{"items": []any{map[string]any{"n": 1.0}, map[string]any{"n": 2.0}}}}, nil)
		req := dynamicRequest(t, map[string]any{"*": map[string]any{"items": []any{map[string]any{"n": 3.0}}}}, nil)

		err := update.MergePaths(proto.Clone(stored), req, []string{"extra_data.*.items.*.n"})
		require.ErrorIs(t, err, update.ErrElementCountMismatch)

		spec, err := update.NewSpec(&testpb.UpdateRequest{})
		require.NoError(t, err)

		_, err = spec.Plan(req, &fieldmaskpb.FieldMask{Paths: []string{"extra_data.*.items.*.n"}}, update.WithCurrent(stored))
		require.ErrorIs(t, err, update.ErrElementCountMismatch)
	})
}
