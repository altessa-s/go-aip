// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// An ordered comparison with null, or of bytes (MongoDB orders BinData by length first), departs from CEL and is rejected; equality
// on both stays translated.
func TestOrderedOperands(t *testing.T) {
	t.Parallel()

	tr := exprTranslator(t, filter.WithFieldTypes(map[string]filter.FieldKind{
		"age": filter.FieldKindInt, "data": filter.FieldKindBytes, "blob": filter.FieldKindBytes,
	}))

	for _, expr := range []string{
		`age > null`, `age >= null`, `age < null`, `age <= null`, `other > null`,
		`data < b"b"`, `data >= b""`, `other > b"a"`, `data < blob`, `data >= blob`,
	} {
		_, err := tr.Translate(filtertest.MustParseFilter(t, expr))
		require.ErrorIs(t, err, filter.ErrUnsupportedOperation, expr)
	}

	for expr, want := range map[string]bson.M{
		`data == b"ab"`: {"data": []byte("ab")},
		`data != b"ab"`: {"data": bson.M{"$ne": []byte("ab")}},
		`age == null`:   {"age": nil},
	} {
		got, err := tr.Translate(filtertest.MustParseFilter(t, expr))
		require.NoError(t, err, expr)
		require.Equal(t, want, got, expr)
	}
}
