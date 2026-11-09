// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

func TestEvaluator_SizeZeroWhenAbsent(t *testing.T) {
	t.Parallel()

	rows := map[string]map[string]any{
		"absent": {},
		"null":   {"name": nil},
		"empty":  {"name": ""},
		"ab":     {"name": "ab"},
		"number": {"name": int64(5)},
		"crossed": {"name": "x", "s": []any{
			map[string]any{"p": []any{"a"}}, map[string]any{"p": []any{"b"}}, map[string]any{"x": int64(1)},
		}},
	}
	zero := mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"note": filter.FieldKindInt})
	withSchema := mustMongoSchema(t, mongo.WithSchemaSizeZeroWhenAbsent(true), zero)
	withoutSchema := mustMongoSchema(t, zero)
	with := mustSchemaEvaluator(t, withSchema)
	without := mustSchemaEvaluator(t, withoutSchema)

	tests := []struct {
		expr          string
		with, without []string // the rows that match
	}{
		{`name.size() == 0`, []string{"absent", "null", "empty"}, []string{"empty"}},
		{`name.size() != 0`, []string{"ab", "number", "crossed"}, []string{"absent", "null", "ab", "number", "crossed"}},
		{`!(name.size() == 0)`, []string{"ab", "number", "crossed"}, []string{"absent", "null", "ab", "number", "crossed"}},
		{`name.size() == 2`, []string{"ab"}, []string{"ab"}},
		{`name.size() < 1`, []string{"absent", "null", "empty"}, []string{"absent", "null", "empty"}},
		// A path through a list of documents is measured as resolved: the list of the values found.
		{`s.p.size() == 2`, []string{"crossed"}, []string{}},
		{`s.p.size() == 1`, []string{}, []string{"crossed"}},
		{`s.p.size() == 0`, []string{"absent", "null", "empty", "ab", "number"}, []string{}},
		// A field declared by WithSchemaZeroWhenAbsent keeps its rule: an absent int is 0, which has no size.
		{`note.size() == 0`, []string{}, []string{}},
		{`note.size() != 0`, []string{"absent", "null", "empty", "ab", "number", "crossed"},
			[]string{"absent", "null", "empty", "ab", "number", "crossed"}},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			t.Parallel()

			node := filtertest.MustParseFilter(t, tt.expr)
			for eval, want := range map[*filtereval.Evaluator][]string{with: tt.with, without: tt.without} {
				got := []string{}

				for name, row := range rows {
					ok, err := eval.Evaluate(node, row)
					require.NoError(t, err)

					if ok {
						got = append(got, name)
					}
				}

				require.ElementsMatch(t, want, got, "with the option: %v", eval == with)
			}
		})
	}
}

func TestTranslatorContext_MeasuresResolvedSize(t *testing.T) {
	t.Parallel()

	ctx, err := filter.NewPolicy(filter.WithArrayFields("tags"), filter.WithAnyField(true))
	require.NoError(t, err)
	semantics := mustMongoSchema(t, mongo.WithSchemaSizeZeroWhenAbsent(true),
		mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"note": filter.FieldKindString}))
	require.True(t, semantics.SizeZeroWhenAbsent())
	require.True(t, semantics.MeasuresResolvedSize("tags", ctx))
	require.True(t, semantics.MeasuresResolvedSize("name", ctx))
	require.False(t, semantics.MeasuresResolvedSize("note", ctx), "a zero-when-absent field keeps its rule")

	bare, err := filter.NewPolicy(filter.WithArrayFields("tags"), filter.WithAnyField(true))
	require.NoError(t, err)
	bareSemantics := mustMongoSchema(t)
	require.False(t, bareSemantics.SizeZeroWhenAbsent())
	require.True(t, bareSemantics.MeasuresResolvedSize("tags", bare))
	require.False(t, bareSemantics.MeasuresResolvedSize("name", bare))
}

func TestEvaluator_OmitEmpty(t *testing.T) {
	t.Parallel()

	empty, hello := "", "hello"
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	doc := func(key string, v any) map[string]any { return map[string]any{"doc": map[string]any{key: v}} }
	semantics := mustMongoSchema(t, mongo.WithSchemaOmitEmpty("note", "tags", "at", "n", "doc.inner", "ptr", "doc.dec", "doc.tp", "doc.arr",
		"doc.st", "doc.big"))
	eval := mustSchemaEvaluator(t, semantics, filter.WithArrayFields("tags"))

	tests := []struct {
		name string
		row  map[string]any
		expr string
		want bool
	}{
		{"empty string is absent", map[string]any{"note": ""}, `note == ""`, false},
		{"empty string is null", map[string]any{"note": ""}, `note == null`, true},
		{"empty string is not present", map[string]any{"note": ""}, `note != null`, false},
		{"empty string has size 0", map[string]any{"note": ""}, `note.size() < 1`, true},
		{"empty string matches no prefix", map[string]any{"note": ""}, `note.startsWith("")`, false},
		{"empty string in [null]", map[string]any{"note": ""}, `note in [null]`, true},
		{"value is present", map[string]any{"note": "hello"}, `note == "hello"`, true},
		{"pointer to empty is stored", map[string]any{"note": &empty}, `note == ""`, true},
		{"pointer to a value", map[string]any{"note": &hello}, `note == "hello"`, true},
		{"nil pointer is absent", map[string]any{"ptr": (*string)(nil)}, `ptr == null`, true},
		{"zero int is absent", map[string]any{"n": int64(0)}, `n == 0`, false},
		{"zero int is null", map[string]any{"n": int64(0)}, `n == null`, true},
		{"zero time is absent", map[string]any{"at": time.Time{}}, `at == null`, true},
		{"empty list is absent", map[string]any{"tags": []string{}}, `tags.size() == 0`, true},
		{"empty list has no element", map[string]any{"tags": []string{}}, `tags != "x"`, true},
		{"nested empty value", map[string]any{"doc": map[string]any{"inner": ""}}, `has(doc.inner)`, false},
		{"nested value", map[string]any{"doc": map[string]any{"inner": "x"}}, `has(doc.inner)`, true},
		{"undeclared empty string is stored", map[string]any{"other": ""}, `other == ""`, true},
		// The driver's rules: IsZero when defined, length for arrays, never a struct.
		{"zero decimal is absent", doc("dec", decimal.Zero), `has(doc.dec)`, false},
		{"nonzero decimal is present", doc("dec", decimal.NewFromInt(1)), `has(doc.dec)`, true},
		{"pointer to zero time is absent", doc("tp", &time.Time{}), `has(doc.tp)`, false},
		{"pointer to a time is present", doc("tp", &at), `has(doc.tp)`, true},
		{"nil time pointer is absent", doc("tp", (*time.Time)(nil)), `has(doc.tp)`, false},
		{"zero array of length 1 is present", doc("arr", [1]int{}), `has(doc.arr)`, true},
		{"array of length 0 is absent", doc("arr", [0]int{}), `has(doc.arr)`, false},
		{"zero struct is present", doc("st", struct{ A int }{}), `has(doc.st)`, true},
		{"large zero array is present", doc("big", [1 << 16]int64{}), `has(doc.big)`, true},
		{"false is absent", doc("st", false), `has(doc.st)`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := eval.Evaluate(filtertest.MustParseFilter(t, tt.expr), tt.row)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}

	require.True(t, semantics.IsOmitEmpty("note"))
	require.False(t, semantics.IsOmitEmpty("other"))
}
