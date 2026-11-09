// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

func TestArrayNormalizationBudget(t *testing.T) {
	t.Parallel()

	s := listing.MustSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "tags", Kind: filter.FieldKindString, Array: true}}, []string{"id"}, listing.NewProfile())
	c := compiler{schema: s, refs: map[string]*fieldRef{}}
	e := contractEvaluator{budget: readBudget{left: 5}, bounded: true, precision: s.TimestampPrecision()}
	e.reader = NewReader(nil, 100).BindBudget(&e.budget)
	got, err := e.field(c.ref("tags"), map[string]any{"tags": []string{"a", "b", "c"}})
	require.NoError(t, err)
	require.Equal(t, []any{"a", "b", "c"}, got)
	require.Zero(t, e.budget.left)
}

func TestNormalizationWithoutBudget(t *testing.T) {
	t.Parallel()

	e := contractEvaluator{}
	value := "a"
	got, err := e.normalizeArray([]*string{&value})
	require.NoError(t, err)
	require.Equal(t, []any{"a"}, got)
}
