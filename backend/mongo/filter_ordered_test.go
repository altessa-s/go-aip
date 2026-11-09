// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestFilterTranslateD_MatchesTranslate(t *testing.T) {
	t.Parallel()

	trans := mustTranslatorWithSchema(t, []SchemaOption{zeroFields()}, filter.WithFieldTypes(map[string]filter.FieldKind{"cost": filter.FieldKindDecimal}))
	for _, expr := range []string{
		`name == "John"`,
		`(age > 3 || tags.size() <= 2) && name.startsWith("a") && !(kind in ["x", "y"])`,
		`cost >= 0.1 && has(row.name) && active`,
		`!active || blob.size() != 0`,
	} {
		node := filtertest.MustParseFilter(t, expr)
		m, err := trans.Translate(node)
		require.NoError(t, err, expr)
		d, err := trans.TranslateD(node)
		require.NoError(t, err, expr)

		// Equal documents: the ordered form only fixes the key order.
		fromM, err := bson.MarshalExtJSON(m, true, false)
		require.NoError(t, err)
		fromD, err := bson.MarshalExtJSON(d, true, false)
		require.NoError(t, err)
		require.JSONEq(t, string(fromM), string(fromD), expr)

		want, err := bson.Marshal(d)
		require.NoError(t, err)

		for range 20 {
			again, err := trans.TranslateD(node)
			require.NoError(t, err)
			got, err := bson.Marshal(again)
			require.NoError(t, err)
			require.Equal(t, want, got, "translating twice yields the same bytes: %s", expr)
		}

		requireOrdered(t, d)
	}
}

func TestFilterTranslateD_Nil(t *testing.T) {
	t.Parallel()

	d, err := mustTranslator(t).TranslateD(nil)
	require.NoError(t, err)
	require.Equal(t, bson.D{}, d)

	_, err = mustTranslator(t, filter.WithAllowedFields("a")).TranslateD(filtertest.MustParseFilter(t, `b == 1`))
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
}

func TestOrderedDocument_SortsKeys(t *testing.T) {
	t.Parallel()

	got := orderedDocument(bson.M{"b": bson.A{bson.M{"z": 1, "y": []any{bson.M{"q": 1, "p": 2}}}}, "a": int64(1)})
	want := bson.D{
		{Key: "a", Value: int64(1)},
		{Key: "b", Value: bson.A{bson.D{
			{Key: "y", Value: bson.A{bson.D{{Key: "p", Value: 2}, {Key: "q", Value: 1}}}},
			{Key: "z", Value: 1},
		}}},
	}
	require.Equal(t, want, got)
}

// requireOrdered fails when a document inside v is not an ordered bson.D.
func requireOrdered(tb testing.TB, v any) {
	tb.Helper()

	switch x := v.(type) {
	case bson.M:
		tb.Fatalf("unordered document %v", x)
	case bson.D:
		for _, e := range x {
			requireOrdered(tb, e.Value)
		}
	case bson.A:
		for _, e := range x {
			requireOrdered(tb, e)
		}
	case []any:
		for _, e := range x {
			requireOrdered(tb, e)
		}
	}
}
