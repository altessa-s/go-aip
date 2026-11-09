// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestValidationChecksSkippedFields(t *testing.T) {
	t.Parallel()

	for _, expression := range []string{`true || hidden == "x"`, `false && hidden == "x"`, `true || has(hidden.value)`, `true || hidden.size() == 0`, `true || name in [hidden]`} {
		t.Run(expression, func(t *testing.T) {
			t.Parallel()

			eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAllowedFields("name")))
			require.NoError(t, err)
			node := filtertest.MustParseFilter(t, expression)
			require.ErrorIs(t, eval.Validate(node), filter.ErrFieldNotAllowed)
			prepared, err := eval.Prepare(node)
			require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
			require.Nil(t, prepared)

			matched, err := eval.Evaluate(node, map[string]any{"name": "x"})
			require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
			require.False(t, matched)
		})
	}
}

func TestAllowedShortCircuitKeepsDataDependentBehavior(t *testing.T) {
	t.Parallel()

	eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAllowedFields("name")))
	require.NoError(t, err)
	node := filtertest.MustParseFilter(t, `name == "x" || name < 1`)
	require.NoError(t, eval.Validate(node))
	prepared, err := eval.Prepare(node)
	require.NoError(t, err)
	matched, err := prepared.Evaluate(map[string]any{"name": "x"})
	require.NoError(t, err)
	require.True(t, matched)
}
