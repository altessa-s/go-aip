// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// nullSafe is a dialect with a native null-safe comparison.
type nullSafe struct{ extended }

func (nullSafe) NullSafeEqual(lhs, rhs string, equal bool) string {
	if equal {
		return lhs + " <=> " + rhs
	}

	return "NOT (" + lhs + " <=> " + rhs + ")"
}

// TestTranslate_Negation pins the pushed-down negation: every predicate renders its own negation so that a NULL field
// matches wherever the evaluator's `!` holds (the predicate is false) and never where the evaluator reports an error.
func TestTranslate_Negation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, expr, want string
		dialect          sqlbase.FilterDialect
		args             []any
	}{
		{"not equal", `name != "a"`, `("name" != ? OR "name" IS NULL)`, extended{}, []any{"a"}},
		{"not equal literal first", `"a" != name`, `(? != "name" OR "name" IS NULL)`, extended{}, []any{"a"}},
		{"negated equal", `!(name == "a")`, `("name" != ? OR "name" IS NULL)`, extended{}, []any{"a"}},
		{"negated not equal", `!(name != "a")`, `"name" = ?`, extended{}, []any{"a"}},
		{"negated ordered", `!(age > 5)`, `NOT ("age" > ?)`, extended{}, []any{int64(5)}},
		{"negated equal null", `!(name == null)`, `"name" IS NOT NULL`, extended{}, nil},
		{"double negation inside", `!(!(name == "a") && age > 5)`, `("name" = ?) OR (NOT ("age" > ?))`, extended{}, []any{"a", int64(5)}},
		{"negated or", `!(name == "a" || active)`, `(("name" != ? OR "name" IS NULL)) AND (NOT ("active" = <literal>))`, extended{}, []any{"a"}},
		{"field equal field", `name == other`, `COALESCE("name" = "other", "name" IS NULL AND "other" IS NULL)`, extended{}, nil},
		{"field not equal field", `name != other`, `COALESCE("name" != "other", ("name" IS NULL) != ("other" IS NULL))`, extended{}, nil},
		{"negated field equal field", `!(name == other)`, `NOT ("name" <=> "other")`, nullSafe{}, nil},
		{"native field equal field", `name == other`, `"name" <=> "other"`, nullSafe{}, nil},
		{"negated field less field", `!(name < other)`, `NOT ("name" < "other")`, nullSafe{}, nil},
		{"field not equal size", `age != size(name)`, `("age" != length("name") OR ("age" IS NULL AND length("name") IS NOT NULL))`, extended{}, nil},
		{"size not equal field", `size(name) != age`, `(length("name") != "age" OR ("age" IS NULL AND length("name") IS NOT NULL))`, extended{}, nil},
		{"field equal size", `age == size(name)`, `"age" = length("name")`, extended{}, nil},
		{"size not equal literal", `size(name) != 3`, `length("name") != ?`, extended{}, []any{int64(3)}},
		{"negated contains", `!name.contains("a")`, `(NOT ("name" ~ ?) OR "name" IS NULL)`, extended{}, []any{"a"}},
		{"negated contains field", `!name.contains(other)`, `(NOT ("name" ~ "other") OR "name" IS NULL)`, extended{}, nil},
		{"negated has", `!has(a.b)`, `"a"."b" IS NULL`, extended{}, nil},
		{"negated bare bool", `!active`, `NOT ("active" = <literal>)`, extended{}, nil},
		{"negated in field list", `!(name in [other, "a"])`, `((NOT ("name" <=> "other")) AND (("name" != ? OR "name" IS NULL)))`, nullSafe{}, []any{"a"}},
		{"negated array equal", `!(tags == "a")`, `("tags" IS NULL OR NOT (any("tags" = ?)))`, extended{}, []any{"a"}},
		{"negated array not equal", `!(tags != "a")`, `any("tags" = ?)`, extended{}, []any{"a"}},
		{"negated array ordered", `!(tags > "a")`, `("tags" IS NULL OR NOT (any("tags" > ?)))`, extended{}, []any{"a"}},
		{"negated element in array", `!("a" in tags)`, `("tags" IS NULL OR NOT (any("tags" = ?)))`, extended{}, []any{"a"}},
		{"negated array in", `!(tags in ["a", null])`, `((("tags" IS NULL OR NOT (any("tags" = ?)))) AND ("tags" IS NOT NULL))`, extended{}, []any{"a"}},
		{"negated array in one", `!(tags in ["a"])`, `(("tags" IS NULL OR NOT (any("tags" = ?))))`, extended{}, []any{"a"}},
		{"negated array in empty", `!(tags in [])`, sqlbase.MatchAll, extended{}, nil},
		{"negated array contains", `!tags.contains("a")`, `("tags" IS NULL OR NOT (exists("tags": e ~ ?)))`, extended{}, []any{"a"}},
		{"negated array has", `!has(a.tags)`, `"a"."tags" IS NULL`, extended{}, nil},
		{"negated empty array equal", `!(tags == "a")`, `NOT (any("tags" = ?))`, emptyArrays{}, []any{"a"}},
		{"negated empty array ordered", `!(tags > "a")`, `NOT (any("tags" > ?))`, emptyArrays{}, []any{"a"}},
		{"negated empty array has", `!has(a.tags)`, `empty("a"."tags")`, emptyArrays{}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			policy := filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithArrayFields("tags", "a.tags"),
				filter.WithFieldTypes(map[string]filter.FieldKind{
					"name": filter.FieldKindString, "other": filter.FieldKindString, "age": filter.FieldKindInt,
					"active": filter.FieldKindBool, "tags": filter.FieldKindString,
				}))

			tr, err := sqlbase.NewFilterTranslator(policy, tt.dialect)
			require.NoError(t, err)

			got, args, err := tr.Translate(filtertest.MustParseFilter(t, tt.expr))
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.args, args)
		})
	}
}

// TestTranslate_NegatedPredicateRenderer: an application predicate is negated with NOT around its rendering.
func TestTranslate_NegatedPredicateRenderer(t *testing.T) {
	t.Parallel()

	tr, err := sqlbase.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("name")), failing{},
		sqlbase.WithFilterPredicateRenderer(renderPredicate))
	require.NoError(t, err)

	out, args, err := tr.Translate(&filter.UnaryOpNode{Op: filter.OpNot,
		Operand: &filter.CallNode{Op: filter.Operator(250), Target: &filter.IdentNode{Name: "name"}}})
	require.NoError(t, err)
	require.Equal(t, "NOT (custom(?))", out)
	require.Equal(t, []any{"opaque"}, args)
}
