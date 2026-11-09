// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestMaskErrorClassification(t *testing.T) {
	t.Parallel()

	resource := &timestamppb.Timestamp{}
	_, err := update.ValidateStrict(fieldmask.FieldMask{"missing": nil}, resource)
	require.ErrorIs(t, err, update.ErrInvalidUpdateMask)
	require.ErrorIs(t, err, fieldmask.ErrInvalidPath)
	path, ok := errors.AsType[*fieldmask.ValidationError](err)
	require.True(t, ok)
	require.Equal(t, "missing", path.Path)

	_, err = update.ValidateStrict(fieldmask.FieldMask{"seconds": nil}, nil)
	require.ErrorIs(t, err, update.ErrMessageMismatch)
	spec, err := update.NewSpec(resource, update.WithIdentityFields("seconds"), update.WithUpdateMaskFieldName("mask"))
	require.NoError(t, err)

	for _, tc := range []struct {
		path  string
		cause error
	}{
		{"missing", fieldmask.ErrInvalidPath},
		{"seconds", update.ErrProtectedField},
		{"bad..path", fieldmask.ErrInvalidPath},
	} {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()

			_, err := spec.Plan(resource, maskOf([]string{tc.path}))
			require.ErrorIs(t, err, tc.cause)
			require.ErrorIs(t, err, coreerrs.ErrInvalidArgument)
			field, ok := errors.AsType[*coreerrs.FieldError](err)
			require.True(t, ok)
			require.Equal(t, "mask.paths[0]", field.Field)
		})
	}
}
