// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

func TestEvaluator_Lookup(t *testing.T) {
	t.Parallel()

	plain, err := newEvaluator()
	require.NoError(t, err)
	got, present, err := plain.Lookup(map[string]any{"meta": map[string]any{"name": "alpha"}}, "meta.name")
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, "alpha", got)

	_, present, err = plain.Lookup(map[string]any{"meta": map[string]string{"name": "alpha"}}, "meta.name")
	require.NoError(t, err)
	require.False(t, present, "the default evaluator steps through map[string]any only")
}

// zeroable is a document that reports itself empty when off is set, as a bson.Zeroer does; without a value encoding it is an
// opaque struct.
type zeroable struct {
	Name string `bson:"name"`
	off  bool
}

func (z zeroable) IsZero() bool { return z.off }

// TestEvaluator_LookupAgreesWithPredicates pins Lookup to what the field's predicates read, for every option combination that
// changes how a field is read: has(field) holds exactly when Lookup reports it present, and field == v holds for the string Lookup
// returns, or for each string of the list it returns.
func TestEvaluator_LookupAgreesWithPredicates(t *testing.T) {
	t.Parallel()

	mapping := filter.WithFieldMapping(map[string]string{"alias.name": "doc.name"})
	combos := map[string][]any{
		"default":              {},
		"default, array field": {withPolicy(filter.WithArrayFields("steps.providerId"))},
		"default, mapped":      {mapping},
	}
	rows := []map[string]any{
		{},
		{"doc": map[string]any{"name": nil}, "steps": nil},
		{"doc": map[string]any{"name": "b"}, "steps": []any{map[string]any{"providerId": "x"}, map[string]any{"providerId": "y"}}},
		{"doc": map[string]any{"name": ""}, "steps": []any{}},
		{"doc": map[string]string{"name": "c"}, "steps": []any{map[string]any{"other": "z"}}},
		{"doc": zeroable{Name: "d", off: true}},
	}
	fields := []string{"doc.name", "alias.name", "steps.providerId"}

	for name, opts := range combos {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			eval, err := newEvaluator(opts...)
			require.NoError(t, err)

			for i, row := range rows {
				for _, field := range fields {
					value, present, err := eval.Lookup(row, field)
					require.NoError(t, err)
					has, err := eval.Evaluate(filtertest.MustParseFilter(t, "has("+field+")"), row)
					require.NoError(t, err)
					require.Equal(t, present, has, "row %d: Lookup(%s) present vs has()", i, field)

					var want []string

					switch v := value.(type) {
					case string:
						want = []string{v}
					case []any:
						for _, el := range v {
							if s, ok := el.(string); ok {
								want = append(want, s)
							}
						}
					}

					for _, s := range want {
						eq, err := eval.Evaluate(filtertest.MustParseFilter(t, field+` == "`+s+`"`), row)
						require.NoError(t, err)
						require.True(t, eq, "row %d: %s == %q for the value Lookup returns", i, field, s)
					}
				}
			}
		})
	}
}

// TestEvaluator_LookupArrayFieldDefaultMode pins that the default evaluator's Lookup reads a declared dotted array field through
// a list of documents, as its predicates and has() do.
func TestEvaluator_LookupArrayFieldDefaultMode(t *testing.T) {
	t.Parallel()

	eval, err := newEvaluator(withPolicy(filter.WithArrayFields("steps.providerId")))
	require.NoError(t, err)

	data := map[string]any{"steps": []any{map[string]any{"providerId": "a"}}}
	v, present, err := eval.Lookup(data, "steps.providerId")
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, []any{"a"}, v)

	small, err := newEvaluator(withPolicy(filter.WithArrayFields("steps.providerId")), filtereval.WithMaxDataBudget(10))
	require.NoError(t, err)
	_, _, err = small.Lookup(map[string]any{"steps": make([]any, 1000)}, "steps.providerId")
	require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
}
