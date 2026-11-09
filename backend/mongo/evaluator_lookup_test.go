// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/pagination/pagetoken"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

func TestEvaluator_Lookup(t *testing.T) {
	t.Parallel()

	inner := map[string]string{"name": "alpha"}
	semantics := mustMongoSchema(t, mongo.WithSchemaOmitEmpty("note"),
		mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"n": filter.FieldKindInt}))
	eval := mustMongoEvaluator(t, filtereval.WithProfile(semantics),
		withPolicy(filter.WithAllowedFields("meta.name", "note", "n", "s.p", "missing", "x")))

	tests := []struct {
		name    string
		data    map[string]any
		field   string
		want    any
		present bool
	}{
		{"typed nested map", map[string]any{"meta": map[string]string{"name": "alpha"}}, "meta.name", "alpha", true},
		{"pointer to a typed map", map[string]any{"meta": &inner}, "meta.name", "alpha", true},
		{"absent", map[string]any{}, "missing", nil, false},
		{"null", map[string]any{"missing": nil}, "missing", nil, true},
		{"omitted empty value", map[string]any{"note": ""}, "note", nil, false},
		{"zero when absent", map[string]any{}, "n", int64(0), true},
		{"through a list", map[string]any{"s": []any{map[string]any{"p": "a"}, map[string]any{"p": "b"}}}, "s.p", []any{"a", "b"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, present, err := eval.Lookup(tt.data, tt.field)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.present, present)
		})
	}

	_, _, err := eval.Lookup(map[string]any{}, "other")
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)

	small := mustMongoEvaluator(t, filtereval.WithMaxDataBudget(10))
	_, _, err = small.Lookup(map[string]any{"x": make([]int, 1000)}, "x")
	require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
}

// TestEvaluator_LookupCyclicValue pins the path of an in-memory sort: Lookup returns a self-referencing value opaque, and
// pagetoken.NewSortValue refuses it instead of looping.
func TestEvaluator_LookupCyclicValue(t *testing.T) {
	t.Parallel()

	var self any

	self = &self
	eval := mustMongoEvaluator(t)
	v, present, err := eval.Lookup(map[string]any{"x": self}, "x")
	require.NoError(t, err)
	require.True(t, present)

	_, err = pagetoken.NewSortValue(v)
	require.ErrorIs(t, err, pagetoken.ErrUnsortableValue)
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
		"mongo":                  {filtereval.WithProfile(mustMongoSchema(t))},
		"mongo, array field":     {filtereval.WithProfile(mustMongoSchema(t)), withPolicy(filter.WithArrayFields("steps.providerId"))},
		"mongo, omit empty":      {filtereval.WithProfile(mustMongoSchema(t, mongo.WithSchemaOmitEmpty("doc.name", "doc", "steps.providerId")))},
		"mongo, size zero":       {filtereval.WithProfile(mustMongoSchema(t, mongo.WithSchemaSizeZeroWhenAbsent(true)))},
		"mongo, mapped and omit": {filtereval.WithProfile(mustMongoSchema(t, mongo.WithSchemaOmitEmpty("doc.name"))), mapping},
		"mongo, all": {filtereval.WithProfile(mustMongoSchema(t, mongo.WithSchemaOmitEmpty("doc"), mongo.WithSchemaSizeZeroWhenAbsent(true))), mapping,
			withPolicy(filter.WithArrayFields("steps.providerId"))},
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
