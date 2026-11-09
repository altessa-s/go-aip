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

func TestAbsentFilter(t *testing.T) {
	t.Parallel()

	e, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)
	matches, err := e.Evaluate(nil, nil)
	require.NoError(t, err)
	require.True(t, matches)

	p, err := e.Prepare(nil)
	require.NoError(t, err)
	matches, err = p.Evaluate(nil)
	require.NoError(t, err)
	require.True(t, matches)
}
