// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/evaluation"
)

// TestPresenceNullInListWithNullElement checks a null presence value is in a list holding null, which only a policy without the
// portable contract accepts.
func TestPresenceNullInListWithNullElement(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString}, {Name: "rating", Kind: filter.FieldKindInt, Presence: true},
	}, []string{"id"}, nil)
	require.NoError(t, err)
	profile, err := evaluation.New(schema, evaluation.NewReader(nil, 0))
	require.NoError(t, err)
	policy, err := filter.NewPolicy(filter.WithAllowedFields("id", "rating"))
	require.NoError(t, err)
	ev, err := evaluator.New(policy, evaluator.WithProfile(profile))
	require.NoError(t, err)

	for _, tc := range []struct {
		expr string
		want bool
	}{
		{`rating in [1, null]`, true}, {`!(rating in [1, null])`, false}, {`rating in [1, 2]`, false}, {`!(rating in [1, 2])`, true},
	} {
		prepared, err := ev.Prepare(filtertest.MustParseFilter(t, tc.expr))
		require.NoError(t, err)
		got, err := prepared.Evaluate(map[string]any{"id": "x", "rating": nil})
		require.NoError(t, err)
		require.Equal(t, tc.want, got, tc.expr)
	}
}
