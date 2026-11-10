// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"
)

// TestApplyMask_OneofSwitch pins the exact case of a path that sets a oneof
// member whose stored sibling is IMMUTABLE: stored items.k.pinned="old",
// request items.k.free="new". Writing free would clear pinned, so ApplyMask,
// Apply and ValidateStrict refuse it with the stored resource, in strict
// mode too, for an explicit, an implied and a full-replacement mask, as
// Spec.Plan does; a stored free is switched to freely.
func TestApplyMask_OneofSwitch(t *testing.T) {
	t.Parallel()

	md := collectionDescriptor(t)
	stored := collectionRoot(md, map[string]map[string]string{"k": {"pinned": "old"}}, nil, nil)
	req := collectionRoot(md, map[string]map[string]string{"k": {"free": "new"}}, nil, nil)

	for _, tc := range []struct {
		name  string
		paths []string
		path  string
	}{
		{name: "explicit", paths: []string{"items.k.free"}, path: "items.k.free"},
		{name: "wildcard sub-path", paths: []string{"items.*.free"}, path: "items.*.free"},
		{name: "implied", paths: nil, path: "items"},
		{name: "full replacement", paths: []string{"*"}, path: "items"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			msg := proto.Clone(req)
			_, err := update.ApplyMask(maskOf(tc.paths), msg, update.WithCurrent(stored))

			if tc.path == "items" { // a collection replaced whole holds new elements, as with Spec.Plan
				require.NoError(t, err)
				return
			}

			want := []FieldViolation{{Path: tc.path, Behavior: annotations.FieldBehavior_IMMUTABLE,
				Reason: `path "` + tc.path + `" would clear "pinned" of its oneof, which an update must not change`}}
			require.Equal(t, want, behaviorViolations(t, err))
			requireProtoEqual(t, req, msg)

			_, err = update.ApplyMask(maskOf(tc.paths), proto.Clone(req), update.WithCurrent(stored), update.WithStrict(true))
			require.ErrorIs(t, err, update.ErrInvalidUpdateMask)
			require.ErrorIs(t, err, update.ErrBehaviorViolation)

			err = update.Apply(fieldmask.FromPaths(tc.paths...), proto.Clone(req), update.WithCurrent(stored))
			require.Equal(t, want, behaviorViolations(t, err))

			_, err = update.ValidateStrict(fieldmask.FromPaths(tc.paths...), req, update.WithCurrent(stored))
			require.ErrorIs(t, err, update.ErrBehaviorViolation)
		})
	}

	t.Run("implied and full replacement of the oneof's message", func(t *testing.T) {
		t.Parallel()

		elem := md.Fields().ByName("list").Message()
		storedElem := dynamicpb.NewMessage(elem)
		storedElem.Set(elem.Fields().ByName("pinned"), protoreflect.ValueOfString("old"))
		reqElem := dynamicpb.NewMessage(elem)
		reqElem.Set(elem.Fields().ByName("free"), protoreflect.ValueOfString("new"))

		for _, paths := range [][]string{nil, {"*"}, {"free"}} {
			msg := proto.Clone(reqElem)
			_, err := update.ApplyMask(maskOf(paths), msg, update.WithCurrent(storedElem))
			require.Equal(t, []FieldViolation{{Path: "free", Behavior: annotations.FieldBehavior_IMMUTABLE,
				Reason: `path "free" would clear "pinned" of its oneof, which an update must not change`}}, behaviorViolations(t, err), "%v", paths)
			requireProtoEqual(t, reqElem, msg)
		}
	})

	t.Run("stored free", func(t *testing.T) {
		t.Parallel()

		free := collectionRoot(md, map[string]map[string]string{"k": {"free": "old"}}, nil, nil)
		got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"items.k.free"}}, proto.Clone(req), update.WithCurrent(free))
		require.NoError(t, err)
		require.Equal(t, []string{"items.k.free"}, got.ToPaths())
	})

	t.Run("without the stored resource", func(t *testing.T) {
		t.Parallel()

		_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"items.k.free"}}, proto.Clone(req))
		require.ErrorIs(t, err, update.ErrBehaviorViolation)
	})
}
