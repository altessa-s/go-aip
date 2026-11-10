// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldmask_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
)

// TestParsePaths_CoveredPath pins that a raw path another path of the same
// mask covers is refused by ParsePaths and FromProtoFieldMask, whichever
// order they come in: the FieldMask could only drop it, and a dropped path
// would escape every later schema and field behavior check. An exact
// duplicate is accepted.
func TestParsePaths_CoveredPath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		paths   []string
		covered string // the refused path; "" when the mask is accepted
		by      string
		want    []string
	}{
		{name: "identifier under its message", paths: []string{"profile", "profile.id"}, covered: "profile.id", by: "profile"},
		{name: "child first", paths: []string{"profile.id", "profile"}, covered: "profile.id", by: "profile"},
		{name: "unknown path under a scalar", paths: []string{"description", "description.no_such"}, covered: "description.no_such", by: "description"},
		{name: "index under its list", paths: []string{"aliases", "aliases.0.id"}, covered: "aliases.0.id", by: "aliases"},
		{name: "wildcard under its list", paths: []string{"aliases", "aliases.*"}, covered: "aliases.*", by: "aliases"},
		{name: "element field under its wildcard", paths: []string{"aliases.*.id", "aliases.*"}, covered: "aliases.*.id", by: "aliases.*"},
		{name: "quoted key", paths: []string{"labels.`a.b`", "labels.`a.b`.id"}, covered: "labels.`a.b`.id", by: "labels.`a.b`"},
		{name: "two levels", paths: []string{"profile", "profile.id.x"}, covered: "profile.id.x", by: "profile"},
		{name: "duplicate", paths: []string{"profile", "profile"}, want: []string{"profile"}},
		{name: "siblings", paths: []string{"profile.id", "profile.display_name"}, want: []string{"profile.display_name", "profile.id"}},
		{name: "prefix that is not a segment", paths: []string{"profile", "profile_id"}, want: []string{"profile", "profile_id"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			parsed, parseErr := fieldmask.ParsePaths(tc.paths...)
			fromProto, protoErr := fieldmask.FromProtoFieldMask(&fieldmaskpb.FieldMask{Paths: tc.paths})

			if tc.covered == "" {
				require.NoError(t, parseErr)
				require.NoError(t, protoErr)
				require.Equal(t, tc.want, parsed.ToPaths())
				require.Equal(t, tc.want, fromProto.ToPaths())

				return
			}

			for _, err := range []error{parseErr, protoErr} {
				requireInvalidPath(t, err, tc.covered)

				ve, _ := errors.AsType[*fieldmask.ValidationError](err)
				require.Equal(t, `path is covered by path "`+tc.by+`" of the same mask`, ve.Reason)
				require.NoError(t, ve.Cause)
			}

			require.Nil(t, parsed)
			require.Nil(t, fromProto)
		})
	}
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
