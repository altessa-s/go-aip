// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/projection"
)

type projectionExpressionResolver struct{}

func (projectionExpressionResolver) Table(name string) (string, error) {
	if name == "tier" {
		return "metadata->>'tier'", nil
	}

	return "", projection.ErrInvalidFieldPath
}

func TestProjectionExternalResolver(t *testing.T) {
	t.Parallel()

	c, err := newContext(projectionExpressionResolver{})
	require.NoError(t, err)
	out, err := sqlbase.TranslateProjection(c, projectionExpressionResolver{}, projection.Spec{Paths: []string{"tier"}})
	require.NoError(t, err)
	require.Equal(t, "metadata->>'tier'", out)
}
