// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// TestKleeneLeavesMongoSemantics checks that the MongoDB profile keeps MongoDB's own semantics: a negated ordering comparison
// matches a missing field, as $not does.
func TestKleeneLeavesMongoSemantics(t *testing.T) {
	t.Parallel()

	schema, err := mongo.NewSchema()
	require.NoError(t, err)
	e, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true)), evaluator.WithProfile(schema))
	require.NoError(t, err)

	for expr, want := range map[string]bool{`x > 1`: false, `!(x > 1)`: true, `x > 1 || t`: true, `!(x > 1 || f)`: true} {
		got, err := e.Evaluate(filtertest.MustParseFilter(t, expr), map[string]any{"t": true, "f": false})
		require.NoError(t, err, expr)
		require.Equal(t, want, got, expr)
	}
}
