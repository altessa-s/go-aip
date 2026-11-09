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

func TestPresenceStrictTranslation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		expr string
		want bson.M
	}{
		{`rating != 1`, bson.M{"rating": bson.M{"$exists": true, "$nin": bson.A{int64(1), nil}}}},
		{`!(rating > 1)`, bson.M{"rating": bson.M{"$lte": int64(1)}}},
		{`!(rating in [1, 2])`, bson.M{"rating": bson.M{"$exists": true, "$nin": bson.A{int64(1), int64(2), nil}}}},
		{`!has(rating.value)`, bson.M{"rating.value": nil}},
		{`!active`, bson.M{"active": false}},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			tr := mustTranslatorWithSchema(t, []SchemaOption{WithSchemaPresenceFields("rating", "score", "active", "rating.value")}, filter.WithFieldTypes(map[string]filter.FieldKind{"rating": filter.FieldKindInt, "score": filter.FieldKindString, "active": filter.FieldKindBool}))
			got, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestPresenceDoesNotRewriteNegationsOfOtherFields(t *testing.T) {
	t.Parallel()
	node := filtertest.MustParseFilter(t, `!(age > 10)`)
	plain := mustTranslatorWithSchema(t, nil)
	withPresence := mustTranslatorWithSchema(t, []SchemaOption{WithSchemaPresenceFields("rating")})
	want, err := plain.Translate(node)
	require.NoError(t, err)
	got, err := withPresence.Translate(node)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, bson.M{"$nor": bson.A{bson.M{"age": bson.M{"$gt": int64(10)}}}}, got)
}

func TestPresenceNegatedDecimalMembershipUsesDecimal128(t *testing.T) {
	t.Parallel()

	first, err := bson.ParseDecimal128("0.1")
	require.NoError(t, err)
	second, err := bson.ParseDecimal128("0.2")
	require.NoError(t, err)
	tr := mustTranslatorWithSchema(t, []SchemaOption{WithSchemaPresenceFields("price")},
		filter.WithFieldTypes(map[string]filter.FieldKind{"price": filter.FieldKindDecimal}))
	got, err := tr.Translate(filtertest.MustParseFilter(t, `!(price in [0.1, "0.2"])`))
	require.NoError(t, err)
	require.Equal(t, bson.M{"price": bson.M{"$exists": true, "$nin": bson.A{first, second, nil}}}, got)
}

func TestPresenceHasRejectsNull(t *testing.T) {
	t.Parallel()
	tr := mustTranslatorWithSchema(t, []SchemaOption{WithSchemaPresenceFields("score")},
		filter.WithFieldTypes(map[string]filter.FieldKind{"score": filter.FieldKindString}))
	got, err := tr.Translate(&filter.CallNode{Op: filter.OpHas, Target: &filter.IdentNode{Name: "score"}})
	require.NoError(t, err)
	require.Equal(t, bson.M{"score": bson.M{"$ne": nil}}, got)
}

func TestPresenceSizeTreatsNullAsZeroWithoutOption(t *testing.T) {
	t.Parallel()
	tr := mustTranslatorWithSchema(t, []SchemaOption{WithSchemaPresenceFields("score")},
		filter.WithFieldTypes(map[string]filter.FieldKind{"score": filter.FieldKindString}))
	got, err := tr.Translate(filtertest.MustParseFilter(t, `!(size(score) == 0)`))
	require.NoError(t, err)

	orEmpty := bson.M{"$ifNull": bson.A{"$score", bson.A{}}}
	stringLength := bson.M{"$cond": bson.A{
		bson.M{"$eq": bson.A{bson.M{"$type": "$score"}, "string"}}, bson.M{"$strLenCP": "$score"}, nil,
	}}
	length := bson.M{"$cond": bson.A{bson.M{"$isArray": orEmpty}, bson.M{"$size": orEmpty}, stringLength}}
	require.Equal(t, bson.M{"$expr": bson.M{"$ne": bson.A{length, int64(0)}}}, got)
}
