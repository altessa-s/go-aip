// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask/update"
)

// maskOf returns the update mask of paths: nil (an omitted update_mask) for
// nil paths, a present mask otherwise, empty for an empty non-nil slice.
func maskOf(paths []string) *fieldmaskpb.FieldMask {
	if paths == nil {
		return nil
	}

	return &fieldmaskpb.FieldMask{Paths: paths}
}

// requireProtectedRefusal preserves the base's distinct aggregate and planning
// error contracts. A protected change is INVALID_ARGUMENT in either form, but
// only Spec/Patch classify it with ErrProtectedField.
func requireProtectedRefusal(t *testing.T, err error, entry string) {
	t.Helper()

	if entry != "ApplyMask" && entry != "Complete" {
		require.ErrorIs(t, err, update.ErrProtectedField)
		return
	}

	require.IsType(t, &update.BehaviorError{}, err)
	require.ErrorIs(t, err, update.ErrBehaviorViolation)
	require.NotErrorIs(t, err, update.ErrProtectedField)

	violations := behaviorViolations(t, err)
	require.NotEmpty(t, violations)

	for _, v := range violations {
		require.Contains(t, []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER}, v.Behavior)
	}
}
