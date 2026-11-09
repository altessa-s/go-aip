// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/projection"
)

func TestDefaultAccess(t *testing.T) {
	t.Parallel()

	access := fieldpolicy.NewAllowList("name")
	_, err := newContext(projection.WithAllowedFields("name", "secret"), projection.WithFieldAccess(access))
	require.ErrorIs(t, err, projection.ErrAllFieldsUnresolvable, "the access policy refuses the allow-list root secret")
	_, err = newContext(projection.WithFieldAccess(access))
	require.ErrorIs(t, err, projection.ErrAllFieldsUnresolvable, "an access policy alone enumerates no field")
	c, err := newContext(projection.WithAllowedFields("name"), projection.WithFieldAccess(access))
	require.NoError(t, err)
	sel, err := c.Resolve(projection.Spec{Mode: projection.ModeAll})
	require.NoError(t, err)
	require.Equal(t, []string{"name"}, sel.Include)
}

func TestNilPathValidators(t *testing.T) {
	t.Parallel()

	p, err := projection.NewParser(projection.WithPathGrammar(nil))
	require.NoError(t, err)
	_, err = p.Parse(t.Context(), "name")
	require.NoError(t, err)
	c, err := newContext(projection.WithPathValidator(nil))
	require.NoError(t, err)
	_, err = c.Resolve(projection.Spec{Paths: []string{"name"}})
	require.NoError(t, err)
}
