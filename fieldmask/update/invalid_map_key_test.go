// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"
)

// TestRejectedKeyPathBesideMapWildcard pins that a path the schema rejects
// under PathValidationWarn or PathValidationDisabled never names a map key
// beside a "*" in Apply: with "delegates.k.typo", "delegates.*" still checks
// k's REQUIRED email and keeps k whole, "delegates.*.email" still reaches
// k, and an invalid key of an integer map ("ports.abc.free") pins no
// wildcard. The rejected path is left out of the cleaned mask.
func TestRejectedKeyPathBesideMapWildcard(t *testing.T) {
	t.Parallel()

	for _, mode := range []update.PathValidationMode{update.PathValidationWarn, update.PathValidationDisabled} {
		opt := update.WithPathValidation(mode)

		err := update.Apply(fieldmask.FromPaths("delegates.*", "delegates.k.typo"),
			delegatesAccount(map[string][2]string{"k": {"", "corp"}}), opt)
		require.Equal(t, []FieldViolation{{Path: "delegates.k.email", Behavior: annotations.FieldBehavior_REQUIRED, Reason: reasonRequired}},
			behaviorViolations(t, err))

		msg := delegatesAccount(map[string][2]string{"k": {"alice@example.com", "corp"}})
		mask := fieldmask.FromPaths("delegates.*", "delegates.k.typo")
		require.NoError(t, update.Apply(mask, msg, opt))
		require.Equal(t, []string{"delegates.*"}, mask.ToPaths())
		requireProtoEqual(t, delegatesAccount(map[string][2]string{"k": {"alice@example.com", "corp"}}), msg)

		msg = delegatesAccount(map[string][2]string{"k": {"new@example.com", "corp"}})
		mask = fieldmask.FromPaths("delegates.*.email", "delegates.k.typo")
		require.NoError(t, update.Apply(mask, msg, opt))
		require.Equal(t, []string{"delegates.*.email"}, mask.ToPaths())
		requireProtoEqual(t, delegatesAccount(map[string][2]string{"k": {"new@example.com", ""}}), msg)

		md := collectionDescriptor(t)
		stored := collectionRoot(md, nil, nil, map[int32]map[string]string{1: {"note": "old"}})
		req := collectionRoot(md, nil, nil, map[int32]map[string]string{1: {"note": "new"}})
		mask = fieldmask.FromPaths("ports.*.note", "ports.abc.free")
		require.NoError(t, update.Apply(mask, proto.Clone(req), opt, update.WithCurrent(stored)))
		require.Equal(t, []string{"ports.*.note"}, mask.ToPaths())
	}
}
