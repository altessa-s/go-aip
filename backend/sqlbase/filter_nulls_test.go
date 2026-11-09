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

// emptyArrays is a dialect whose array columns cannot hold NULL.
type emptyArrays struct{ extended }

func (emptyArrays) ArrayEmpty(col string, empty bool) string {
	if empty {
		return "empty(" + col + ")"
	}

	return "notEmpty(" + col + ")"
}

func kindsPolicy(tb testing.TB) *filter.Policy {
	tb.Helper()

	return filtertest.MustPolicy(tb, filter.WithAnyField(true), filter.WithArrayFields("tags"),
		filter.WithFieldTypes(map[string]filter.FieldKind{
			"name": filter.FieldKindString, "other": filter.FieldKindString, "age": filter.FieldKindInt,
			"score": filter.FieldKindFloat, "price": filter.FieldKindDecimal, "active": filter.FieldKindBool,
			"tags": filter.FieldKindString,
		}))
}

func TestTranslate_NullSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, expr, want string
		dialect          sqlbase.FilterDialect
		args             []any
	}{
		{"in with null", `note in ["x", null]`, `("note" IN (?) OR "note" IS NULL)`, extended{}, []any{"x"}},
		{"in only null", `note in [null]`, `"note" IS NULL`, extended{}, nil},
		{"in nulls", `note in [null, "x", null, "y"]`, `("note" IN (?, ?) OR "note" IS NULL)`, extended{}, []any{"x", "y"}},
		{"not in with null", `!(note in ["x", null])`, `"note" NOT IN (?)`, extended{}, []any{"x"}},
		{"not in", `!(note in ["x", "y"])`, `("note" NOT IN (?, ?) OR "note" IS NULL)`, extended{}, []any{"x", "y"}},
		{"not in only null", `!(note in [null])`, `"note" IS NOT NULL`, extended{}, nil},
		{"not in empty", `!(note in [])`, sqlbase.MatchAll, extended{}, nil},
		{"in field and null", `note in [other, null]`, `((COALESCE("note" = "other", "note" IS NULL AND "other" IS NULL)) OR ("note" IS NULL))`, extended{}, nil},
		{"size in with null", `size(note) in [1, null]`, `length("note") IN (?)`, extended{}, []any{int64(1)}},
		{"size in only null", `size(note) in [null]`, sqlbase.MatchNone, extended{}, nil},
		{"not size in only null", `!(size(note) in [null])`, `length("note") IS NOT NULL`, extended{}, nil},
		{"not size in empty", `!(size(note) in [])`, `length("note") IS NOT NULL`, extended{}, nil},
		{"size equal null", `size(note) == null`, sqlbase.MatchNone, extended{}, nil},
		{"size not equal null", `null != size(note)`, `length("note") IS NOT NULL`, extended{}, nil},
		{"array in with null", `tags in ["a", null]`, `((any("tags" = ?)) OR ("tags" IS NULL))`, extended{}, []any{"a"}},
		{"array equal null", `tags == null`, `"tags" IS NULL`, extended{}, nil},
		{"array not equal null", `tags != null`, `"tags" IS NOT NULL`, extended{}, nil},
		{"array not equal", `tags != "a"`, `("tags" IS NULL OR NOT (any("tags" = ?)))`, extended{}, []any{"a"}},
		{"empty array equal null", `tags == null`, `empty("tags")`, emptyArrays{}, nil},
		{"empty array not equal null", `null != tags`, `notEmpty("tags")`, emptyArrays{}, nil},
		{"empty array not equal", `tags != "a"`, `NOT (any("tags" = ?))`, emptyArrays{}, []any{"a"}},
		{"empty array in with null", `tags in ["a", null]`, `((any("tags" = ?)) OR (empty("tags")))`, emptyArrays{}, []any{"a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tr, err := sqlbase.NewFilterTranslator(kindsPolicy(t), tt.dialect)
			require.NoError(t, err)

			got, args, err := tr.Translate(filtertest.MustParseFilter(t, tt.expr))
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.args, args)
		})
	}
}

func TestTranslate_ArrayExists(t *testing.T) {
	t.Parallel()

	node := &filter.CallNode{Op: filter.OpHas, Target: &filter.IdentNode{Name: "tags"}}

	for dialect, want := range map[sqlbase.FilterDialect]string{extended{}: `"tags" IS NOT NULL`, emptyArrays{}: `notEmpty("tags")`} {
		tr, err := sqlbase.NewFilterTranslator(kindsPolicy(t), dialect)
		require.NoError(t, err)

		got, _, err := tr.Translate(node)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

func TestTranslate_FieldKinds(t *testing.T) {
	t.Parallel()

	tr, err := sqlbase.NewFilterTranslator(kindsPolicy(t), extended{})
	require.NoError(t, err)

	for _, expr := range []string{
		`name == other`, `age < score`, `price >= age`, `size(name) == age`, `name.substring(1) == other`,
		`name == unknown`, `unknown < age`, `name in [other, "x"]`,
	} {
		_, _, err = tr.Translate(filtertest.MustParseFilter(t, expr))
		require.NoError(t, err, expr)
	}

	for _, expr := range []string{
		`name == age`, `active != score`, `size(name) == other`, `name.substring(1) == age`, `age in [name]`,
	} {
		_, _, err = tr.Translate(filtertest.MustParseFilter(t, expr))
		require.ErrorIs(t, err, filter.ErrFieldTypeMismatch, expr)
	}
}

func TestTranslate_DeniedBeforeShape(t *testing.T) {
	t.Parallel()

	policy := filtertest.MustPolicy(t, filter.WithAllowedFields("name"))

	tr, err := sqlbase.NewFilterTranslator(policy, extended{})
	require.NoError(t, err)

	for _, expr := range []string{`secret > null`, `null <= secret`, `secret in ["x", null]`, `name == secret`} {
		_, _, err = tr.Translate(filtertest.MustParseFilter(t, expr))
		require.ErrorIs(t, err, filter.ErrFieldNotAllowed, expr)
	}
}
