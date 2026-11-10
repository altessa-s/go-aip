// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldmask_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
)

// TestParsePaths_CoveredPathCollapses pins that ParsePaths and
// FromProtoFieldMask accept a raw path another path of the same mask covers
// and collapse it into the covering path, whichever order they come in, after
// checking its grammar.
func TestParsePaths_CoveredPathCollapses(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		paths []string
		want  []string
	}{
		{name: "identifier under its message", paths: []string{"profile", "profile.id"}, want: []string{"profile"}},
		{name: "child first", paths: []string{"profile.id", "profile"}, want: []string{"profile"}},
		{name: "unknown path under a scalar", paths: []string{"description", "description.no_such"}, want: []string{"description"}},
		{name: "index under its list", paths: []string{"aliases", "aliases.0.id"}, want: []string{"aliases"}},
		{name: "wildcard under its list", paths: []string{"aliases", "aliases.*"}, want: []string{"aliases"}},
		{name: "quoted key", paths: []string{"labels.`a.b`", "labels.`a.b`.id"}, want: []string{"labels.`a.b`"}},
		{name: "duplicate", paths: []string{"profile", "profile"}, want: []string{"profile"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			parsed, err := fieldmask.ParsePaths(tc.paths...)
			require.NoError(t, err)
			require.Equal(t, tc.want, parsed.ToPaths())

			fromProto, err := fieldmask.FromProtoFieldMask(&fieldmaskpb.FieldMask{Paths: tc.paths})
			require.NoError(t, err)
			require.Equal(t, tc.want, fromProto.ToPaths())
		})
	}

	// The grammar of a covered path is still checked.
	_, err := fieldmask.ParsePaths("profile", "profile..id")
	requireInvalidPath(t, err, "profile..id")
}

// TestValidationError_MaskLevel pins the message of a mask-level error, which
// names no path: no empty path and no leading separator is rendered.
func TestValidationError_MaskLevel(t *testing.T) {
	t.Parallel()

	_, err := fieldmask.FromProtoFieldMask(&fieldmaskpb.FieldMask{Paths: []string{"a", "b", "c"}}, fieldmask.WithMaxPaths(2))
	require.ErrorIs(t, err, fieldmask.ErrMaxPathsExceeded)
	require.Equal(t, "invalid field mask: the mask has 3 paths, more than the limit of 2", err.Error())

	require.Equal(t, "invalid field mask path 'a': r", (&fieldmask.ValidationError{Path: "a", Reason: "r"}).Error())
}
