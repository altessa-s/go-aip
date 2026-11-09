// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/projection"
)

func TestDeniedFieldUnderExactSubtreeMapping(t *testing.T) {
	t.Parallel()

	opts := []any{
		projection.WithFieldMapping(map[string]string{"credentials.": "private.", "alias": "private.password"}),
		projection.WithDeniedFields("credentials.password"),
	}
	ctx, err := newContext(opts...)
	require.NoError(t, err)
	_, err = ctx.Resolve(projection.Spec{Paths: []string{"alias"}})
	require.ErrorIs(t, err, projection.ErrFieldNotAllowed)
	sel, err := ctx.Resolve(projection.Spec{})
	require.NoError(t, err)
	require.Equal(t, []string{"credentials.password", "private.password"}, sel.Exclude)

	opts = append(opts, projection.WithRequiredFields("private.password"))
	_, err = newContext(opts...)
	require.ErrorIs(t, err, projection.ErrConflictingPolicy)
}
