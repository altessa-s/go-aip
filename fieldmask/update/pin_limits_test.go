// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// pinnedAccount returns stored and request Accounts holding delegate k with
// an unchanged org and the extra delegates, each with a new email.
func pinnedAccount(extra ...string) (stored, req *pb.Account) {
	stored = delegatesAccount(map[string][2]string{"k": {"k@example.com", "ko"}})
	req = delegatesAccount(map[string][2]string{"k": {"", "ko"}})

	for _, key := range extra {
		stored.Delegates[key] = &pb.Owner{Email: "old@example.com"}
		req.Delegates[key] = &pb.Owner{Email: "new@example.com", Note: "n"}
	}

	return stored, req
}

// numberedKeys returns n distinct map keys.
func numberedKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = "e" + strconv.Itoa(i)
	}

	return keys
}

// TestPinnedWildcardLimits pins that a "*" pinned to the request keys
// beside a key path the check drops is refused, before anything is copied
// or mutated, when the generated paths would break a mask limit — so the
// cleaned mask and Plan.Paths can always be written back and merged: the
// exact case of 1,001 other entries under the default 1,000 paths, a
// wildcard of several leaves times many entries, a key longer than the path
// length limit, and a cleaned mask whose total count exceeds the limit.
func TestPinnedWildcardLimits(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		paths []string
		keys  []string
		opts  []update.Option
		cause error
		path  string
	}{
		{name: "1,001 other entries", paths: []string{"delegates.*.email", "delegates.k.org"}, keys: numberedKeys(1001),
			cause: fieldmask.ErrMaxPathsExceeded, path: "delegates.*"},
		{name: "leaves times entries", paths: []string{"delegates.*.email", "delegates.*.note", "delegates.k.org"}, keys: numberedKeys(600),
			cause: fieldmask.ErrMaxPathsExceeded, path: "delegates.*"},
		{name: "long key", paths: []string{"delegates.*.email", "delegates.k.org"}, keys: []string{strings.Repeat("x", 1100)},
			cause: fieldmask.ErrMaxPathLengthExceeded, path: "delegates.*"},
		{name: "total after pinning", paths: []string{"delegates.*.email", "delegates.k.org", "display_name"}, keys: numberedKeys(3),
			opts: []update.Option{update.WithMaxPaths(3)}, cause: fieldmask.ErrMaxPathsExceeded, path: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stored, req := pinnedAccount(tc.keys...)
			req.DisplayName = "d"

			opts := append([]update.Option{update.WithCurrent(stored)}, tc.opts...)

			msg := proto.Clone(req)
			got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: tc.paths}, msg, opts...)
			require.Nil(t, got)
			require.ErrorIs(t, err, tc.cause)
			requireValidationPath(t, err, tc.path)
			requireProtoEqual(t, req, msg)

			msg = proto.Clone(req)
			err = update.Apply(fieldmask.FromPaths(tc.paths...), msg, opts...)
			require.ErrorIs(t, err, tc.cause)
			requireProtoEqual(t, req, msg)

			_, err = update.ValidateStrict(fieldmask.FromPaths(tc.paths...), req, opts...)
			require.ErrorIs(t, err, update.ErrInvalidUpdateMask)
			require.ErrorIs(t, err, tc.cause)

			spec, err := update.NewSpec(&pb.Account{}, tc.opts...)
			require.NoError(t, err)

			_, err = spec.Plan(req, &fieldmaskpb.FieldMask{Paths: tc.paths}, update.WithCurrent(stored))
			require.ErrorIs(t, err, tc.cause)

			_, ok := errors.AsType[*update.MaskPathError](err)
			require.True(t, ok, "%v", err)
		})
	}

	// Within the limits the "*" is pinned as before.
	stored, req := pinnedAccount(numberedKeys(2)...)
	got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"delegates.*.email", "delegates.k.org"}}, req, update.WithCurrent(stored))
	require.NoError(t, err)
	require.Equal(t, []string{"delegates.e0.email", "delegates.e1.email"}, got.ToPaths())
}
