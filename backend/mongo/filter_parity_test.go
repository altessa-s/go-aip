// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

// The translator halves of the MongoDB parity regressions of the filter
// package: each case pins the emitted query and the evaluator's answer under
// the same MongoDB semantics profile, over plain Go documents.

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

func mustSchema(tb testing.TB, opts ...mongo.SchemaOption) *mongo.Schema {
	tb.Helper()

	schema, err := mongo.NewSchema(opts...)
	require.NoError(tb, err)

	return schema
}

// mustEvaluator is the MongoDB-semantics evaluator of the parity tests, over any field.
func mustEvaluator(tb testing.TB, schema *mongo.Schema) *filtereval.Evaluator {
	tb.Helper()

	eval, err := filtereval.New(filtertest.MustPolicy(tb, filter.WithAnyField(true)), filtereval.WithProfile(schema))
	require.NoError(tb, err)

	return eval
}

// These outcomes follow the document-wide {s.p:null} guard in the emitted query,
// including the empty and mixed traversals from the MongoDB 7 parity failure.
func TestMongoParityNullGuard(t *testing.T) {
	t.Parallel()
	schema := mustSchema(t, mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"s.p": filter.FieldKindInt}))
	eval := mustEvaluator(t, schema)
	trans, err := mongo.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), schema)
	require.NoError(t, err)

	rows := []map[string]any{
		{}, {"s": nil}, {"s": []any{}}, {"s": []any{5}},
		{"s": []any{[]any{map[string]any{"p": 5}}}},
		{"s": []any{map[string]any{"p": 5}, map[string]any{}}},
		{"s": []any{map[string]any{"p": 5}, map[string]any{"p": nil}}},
		{"s": map[string]any{"p": []any{5, nil}}},
		{"s": map[string]any{"p": 5}},
	}

	for _, tc := range []struct {
		expr  string
		query bson.M
		want  []bool
	}{
		{"s.p == 0", bson.M{"$or": bson.A{bson.M{"s.p": int64(0)}, bson.M{"s.p": nil}}}, []bool{true, true, false, false, false, true, true, true, false}},
		{"s.p > 0", bson.M{"$and": bson.A{bson.M{"s.p": bson.M{"$gt": int64(0)}}, bson.M{"s.p": bson.M{"$ne": nil}}}}, []bool{false, false, false, false, false, false, false, false, true}},
		{"s.p != 0", bson.M{"$and": bson.A{bson.M{"s.p": bson.M{"$ne": int64(0)}}, bson.M{"s.p": bson.M{"$ne": nil}}}}, []bool{false, false, true, true, true, false, false, false, true}},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tc.expr)
			query, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tc.query, query)

			for i, row := range rows {
				got, err := eval.Evaluate(node, row)
				require.NoError(t, err)
				require.Equal(t, tc.want[i], got, "row %d: %#v", i, row)
			}
		})
	}
}

// Pin resolved size semantics separately from the document-wide null guard:
// missing children are skipped, null children count as elements, and a declared
// zero-when-absent field retains its own rule even with SizeZeroWhenAbsent.
func TestMongoParitySizeZeroWhenAbsent(t *testing.T) {
	t.Parallel()
	schema := mustSchema(t, mongo.WithSchemaSizeZeroWhenAbsent(true),
		mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{
			"note": filter.FieldKindString, "z.p": filter.FieldKindInt,
		}))
	eval := mustEvaluator(t, schema)
	trans, err := mongo.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), schema)
	require.NoError(t, err)

	for _, tc := range []struct {
		name  string
		field string
		row   map[string]any
		size  int // -1 means the value has no size
	}{
		{"absent", "name", map[string]any{}, 0},
		{"null", "name", map[string]any{"name": nil}, 0},
		{"unicode", "name", map[string]any{"name": "é😀"}, 2},
		{"number", "name", map[string]any{"name": int64(7)}, -1},
		{"empty traversal", "s.p", map[string]any{"s": []any{}}, 0},
		{"missing child", "s.p", map[string]any{"s": []any{map[string]any{}}}, 0},
		{"mixed children", "s.p", map[string]any{"s": []any{map[string]any{"p": "a"}, map[string]any{}}}, 1},
		{"null child", "s.p", map[string]any{"s": []any{map[string]any{"p": nil}}}, 1},
		{"absent string zero", "note", map[string]any{}, 0},
		{"null string zero", "note", map[string]any{"note": nil}, 0},
		{"absent int zero", "z.p", map[string]any{}, -1},
		{"null int zero", "z.p", map[string]any{"z": map[string]any{"p": nil}}, -1},
		{"guarded mixed children", "z.p", map[string]any{"z": []any{map[string]any{"p": 5}, map[string]any{}}}, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, comparison := range []struct {
				expr string
				want bool
			}{
				{" == 0", tc.size == 0},
				{" != 0", tc.size != 0},
				{" < 1", tc.size == 0},
				{" >= 1", tc.size >= 1},
				{" == 2", tc.size == 2},
				{" != 2", tc.size != 2},
				{" == 0.5", false},
			} {
				node := filtertest.MustParseFilter(t, tc.field+".size()"+comparison.expr)
				_, err := trans.Translate(node)
				require.NoError(t, err)
				got, err := eval.Evaluate(node, tc.row)
				require.NoError(t, err)
				require.Equal(t, comparison.want, got, "%s", comparison.expr)
			}
		})
	}
}
