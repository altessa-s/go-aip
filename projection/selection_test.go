// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/projection"
)

func TestSelectionModes(t *testing.T) {
	t.Parallel()

	c, err := newContext(projection.WithView(projection.ViewBasic, "name"))
	require.NoError(t, err)
	omitted, err := c.Resolve(projection.Spec{})
	require.NoError(t, err)
	require.True(t, omitted.IsAll(), "an omitted mask ignores the views")

	all, err := c.Resolve(projection.Spec{Mode: projection.ModeAll})
	require.NoError(t, err)
	require.True(t, all.IsAll())

	none, err := c.Resolve(projection.Spec{Mode: projection.ModeNone})
	require.NoError(t, err)
	require.False(t, none.IsAll())
	require.False(t, (projection.Spec{Mode: projection.ModeNone}).Contains("name"))
}

func TestCustomPathGrammar(t *testing.T) {
	t.Parallel()

	grammar := func(path string) error {
		if strings.Contains(path, "$") {
			return projection.ErrInvalidFieldPath
		}

		return nil
	}
	p, err := projection.NewParser(projection.WithPathGrammar(grammar), projection.WithRejectDuplicates(true))
	require.NoError(t, err)
	spec, err := p.Parse(t.Context(), "items.0.name")
	require.NoError(t, err)
	c, err := newContext(projection.WithPathValidator(grammar))
	require.NoError(t, err)
	sel, err := c.Resolve(spec)
	require.NoError(t, err)
	require.Equal(t, []string{"items.0.name"}, sel.Include)
	_, err = p.Parse(t.Context(), "a,a")
	require.ErrorIs(t, err, projection.ErrDuplicatePath)
}

func TestRelativePreservesExplicitSelection(t *testing.T) {
	t.Parallel()

	relative, ok := (projection.Spec{Paths: []string{"items"}}).Relative("items")
	require.True(t, ok)
	require.Equal(t, projection.ModeAll, relative.Mode)

	context, err := newContext(projection.WithView(projection.ViewBasic, "name"), projection.WithDefaultView(projection.ViewBasic))
	require.NoError(t, err)
	selection, err := context.Resolve(relative)
	require.NoError(t, err)
	require.True(t, selection.IsAll())
}
