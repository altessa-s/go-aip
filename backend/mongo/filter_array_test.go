// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// arrayLength is the JSON of the size expression the translator emits for field.
func arrayLength(tb testing.TB, field string) any {
	tb.Helper()

	b, err := json.Marshal(sizeLength(field))
	require.NoError(tb, err)

	var v any
	require.NoError(tb, json.Unmarshal(b, &v))

	return v
}

func TestTranslator_ArrayFields(t *testing.T) {
	t.Parallel()

	opts := []any{
		filter.WithArrayFields("tags", "steps.providerId"),
		filter.WithFieldTypes(map[string]filter.FieldKind{"tags": filter.FieldKindString}),
		filter.WithFieldMapping(map[string]string{"tags": "t"}),
	}

	tests := []struct {
		expr string
		want bson.M
	}{
		{`"red" in tags`, bson.M{"t": "red"}},
		{`!("red" in tags)`, bson.M{"$nor": bson.A{bson.M{"t": "red"}}}},
		{`tags == "red"`, bson.M{"t": "red"}},
		{`tags != "red"`, bson.M{"t": bson.M{"$ne": "red"}}},
		{`tags in ["a", "b"]`, bson.M{"t": bson.M{"$in": []any{"a", "b"}}}},
		{`tags.startsWith("r")`, bson.M{"t": bson.M{"$regex": "^r"}}},
		{`tags.size() == 0`, bson.M{"$expr": bson.M{"$eq": bson.A{sizeLength("t"), int64(0)}}}},
		{`tags.size() != 2`, bson.M{"$expr": bson.M{"$ne": bson.A{sizeLength("t"), int64(2)}}}},
		{`steps.providerId.size() == 2`, bson.M{"$expr": bson.M{"$eq": bson.A{sizeLength("steps.providerId"), int64(2)}}}},
		{`tags.size() > 1`, bson.M{"$expr": bson.M{"$and": bson.A{
			bson.M{"$ne": bson.A{sizeLength("t"), nil}},
			bson.M{"$gt": bson.A{sizeLength("t"), int64(1)}},
		}}}},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			t.Parallel()
			got, err := mustTranslator(t, opts...).Translate(filtertest.MustParseFilter(t, tt.expr))
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}

	_, err := mustTranslator(t, opts...).Translate(filtertest.MustParseFilter(t, `1 in tags`))
	require.ErrorIs(t, err, filter.ErrFieldTypeMismatch, "the element is checked against the declared kind")

	// Without the declaration `x in field` has no field on the left.
	_, err = mustTranslator(t).Translate(filtertest.MustParseFilter(t, `"red" in tags`))
	require.ErrorIs(t, err, filter.ErrInvalidExpression)

	semantics, err := NewSchema(WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"tags": filter.FieldKindString}))
	require.NoError(t, err)
	_, err = NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithArrayFields("tags"), filter.WithAnyField(true)), semantics)
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
}

func TestTranslator_ArraySizeLengthShape(t *testing.T) {
	t.Parallel()

	// Absent and null read as an empty array, an array by its count, a string by its code points.
	want := map[string]any{"$cond": []any{
		map[string]any{"$isArray": map[string]any{"$ifNull": []any{"$tags", []any{}}}},
		map[string]any{"$size": map[string]any{"$ifNull": []any{"$tags", []any{}}}},
		map[string]any{"$cond": []any{
			map[string]any{"$eq": []any{map[string]any{"$type": "$tags"}, "string"}},
			map[string]any{"$strLenCP": "$tags"},
			nil,
		}},
	}}
	require.Equal(t, want, arrayLength(t, "tags"))
}

func TestTranslator_SizeOperandRange(t *testing.T) {
	t.Parallel()

	for i, opts := range [][]any{nil, nil, {filter.WithArrayFields("name")}} {
		var semanticsOpts []SchemaOption
		if i == 1 {
			semanticsOpts = []SchemaOption{zeroFields()}
		}

		trans := mustTranslatorWithSchema(t, semanticsOpts, opts...)
		for _, expr := range []string{
			`name.size() == -1`,
			`name.size() != 2147483648`,
			`name.size() > -1`,
			`name.size() < 18446744073709551615u`,
			`size(name) >= -0.5`,
		} {
			_, err := trans.Translate(filtertest.MustParseFilter(t, expr))
			require.ErrorIs(t, err, filter.ErrInvalidExpression, expr)
		}

		for _, expr := range []string{`name.size() == 0`, `name.size() <= 2147483647`, `name.size() < 2u`, `name.size() <= 0.5`} {
			_, err := trans.Translate(filtertest.MustParseFilter(t, expr))
			require.NoError(t, err, expr)
		}
	}
}

// TestTranslator_FractionalSizeOperand pins that a fractional size() operand never reaches $size, which MongoDB rejects: == matches
// nothing and != everything, as the evaluator decides; the ordering operators keep their $expr forms.
func TestTranslator_FractionalSizeOperand(t *testing.T) {
	t.Parallel()

	for _, opts := range [][]any{nil, {filter.WithArrayFields("tags")}} {
		tr := mustTranslator(t, opts...)
		for expr, want := range map[string]bson.M{
			`tags.size() == 0.5`:    {"$expr": false},
			`tags.size() != 0.5`:    {"$expr": true},
			`!(tags.size() == 0.5)`: {"$nor": bson.A{bson.M{"$expr": false}}},
		} {
			got, err := tr.Translate(filtertest.MustParseFilter(t, expr))
			require.NoError(t, err, expr)
			require.Equal(t, want, got, expr)
		}

		got, err := tr.Translate(filtertest.MustParseFilter(t, `tags.size() == 2.0`))
		require.NoError(t, err)
		require.NotContains(t, bsonToJSON(got), `"$expr":false`, "a whole operand is a size")
		got, err = tr.Translate(filtertest.MustParseFilter(t, `tags.size() < 0.5`))
		require.NoError(t, err)
		require.Contains(t, bsonToJSON(got), `"$lt"`)
	}
}

// TestTranslator_SizeCallInFieldPosition pins that size() stands for a field only on the left of a comparison: as the target of
// in, a string function or another size() it is rejected rather than read as its field.
func TestTranslator_SizeCallInFieldPosition(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		`tags.size() in [1, 2]`,
		`tags.size().contains("1")`,
		`tags.size().size() == 1`,
	} {
		_, err := mustTranslator(t).Translate(filtertest.MustParseFilter(t, expr))
		require.ErrorIs(t, err, filter.ErrUnsupportedOperation, expr)
	}
}

func TestTranslator_SizeZeroWhenAbsent(t *testing.T) {
	t.Parallel()

	semanticsOpts := []SchemaOption{
		WithSchemaSizeZeroWhenAbsent(true),
		WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"note": filter.FieldKindString}),
	}
	policyOpts := []any{filter.WithFieldMapping(map[string]string{"name": "n"})}

	tests := []struct {
		expr string
		want bson.M
	}{
		{`name.size() == 0`, bson.M{"$expr": bson.M{"$eq": bson.A{sizeLength("n"), int64(0)}}}},
		{`name.size() != 2`, bson.M{"$expr": bson.M{"$ne": bson.A{sizeLength("n"), int64(2)}}}},
		{`s.p.size() == 1`, bson.M{"$expr": bson.M{"$eq": bson.A{sizeLength("s.p"), int64(1)}}}},
		{`name.size() > 1`, bson.M{"$expr": bson.M{"$and": bson.A{
			bson.M{"$ne": bson.A{sizeLength("n"), nil}},
			bson.M{"$gt": bson.A{sizeLength("n"), int64(1)}},
		}}}},
		// A zero-when-absent field keeps its own rule.
		{`note.size() == 0`, bson.M{"$or": bson.A{
			bson.M{"$or": bson.A{
				bson.M{"note": bson.M{"$size": int64(0)}},
				bson.M{"$expr": bson.M{"$eq": bson.A{stringLength("note"), int64(0)}}},
			}},
			bson.M{"note": nil},
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			t.Parallel()
			got, err := mustTranslatorWithSchema(t, semanticsOpts, policyOpts...).Translate(filtertest.MustParseFilter(t, tt.expr))
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTranslator_ArrayNullAndHas(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want bson.M
	}{
		{`has(row.tags)`, bson.M{"row.tags.0": bson.M{"$exists": true}}},
		{`row.tags == null`, bson.M{"row.tags.0": bson.M{"$exists": false}}},
		{`row.tags != null`, bson.M{"row.tags.0": bson.M{"$exists": true}}},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			trans := mustTranslatorWithSchema(t, nil, filter.WithArrayFields("row.tags"), filter.WithPortableContract(true))
			got, err := trans.Translate(filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestTranslator_ArrayNullAndHasKeepDefaultSemantics(t *testing.T) {
	t.Parallel()

	trans := mustTranslatorWithSchema(t, nil, filter.WithArrayFields("row.tags"))
	for expr, want := range map[string]bson.M{
		`has(row.tags)`:    {"row.tags": bson.M{"$exists": true}},
		`row.tags == null`: {"row.tags": nil},
		`row.tags != null`: {"row.tags": bson.M{"$ne": nil}},
	} {
		got, err := trans.Translate(filtertest.MustParseFilter(t, expr))
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}
