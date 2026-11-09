// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package bsonbound

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// testNesting is the nesting limit of the tests.
const testNesting = 102

// encodeBounded encodes doc through a bounded writer of the given limit.
func encodeBounded(tb testing.TB, doc any, limit int) (*bytes.Buffer, *Bound, error) {
	tb.Helper()

	var buf bytes.Buffer

	vw, bound := NewValueWriter(bson.NewDocumentWriter(&buf), limit, testNesting, nil)
	err := bson.NewEncoder(vw).Encode(doc)

	return &buf, bound, err
}

func TestBoundedWriterMeasuresExactSize(t *testing.T) {
	t.Parallel()

	doc := bson.D{
		{Key: "string", Value: "text"},
		{Key: "int32", Value: int32(1)},
		{Key: "int64", Value: int64(2)},
		{Key: "double", Value: 1.5},
		{Key: "bool", Value: true},
		{Key: "binary", Value: bson.Binary{Data: []byte{1, 2, 3}}},
		{Key: "old", Value: bson.Binary{Subtype: bson.TypeBinaryBinaryOld, Data: []byte{1, 2}}},
		{Key: "date", Value: bson.DateTime(3)},
		{Key: "decimal", Value: bson.NewDecimal128(1, 2)},
		{Key: "oid", Value: bson.ObjectID{1}},
		{Key: "regex", Value: bson.Regex{Pattern: "a+", Options: "i"}},
		{Key: "js", Value: bson.JavaScript("f()")},
		{Key: "symbol", Value: bson.Symbol("s")},
		{Key: "ts", Value: bson.Timestamp{T: 1, I: 2}},
		{Key: "pointer", Value: bson.DBPointer{DB: "db.c", Pointer: bson.ObjectID{2}}},
		{Key: "null", Value: nil},
		{Key: "min", Value: bson.MinKey{}},
		{Key: "max", Value: bson.MaxKey{}},
		{Key: "undefined", Value: bson.Undefined{}},
		{Key: "doc", Value: bson.D{{Key: "a", Value: bson.A{1, "b", bson.D{}}}}},
		{Key: "array", Value: bson.A{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}},
		{Key: "scope", Value: bson.CodeWithScope{Code: "g()", Scope: bson.D{{Key: "x", Value: int32(1)}}}},
	}

	buf, bound, err := encodeBounded(t, doc, filtereval.MaxDocumentBytes)
	require.NoError(t, err)
	require.Equal(t, buf.Len(), bound.size)
	require.Zero(t, bound.depth)

	raw, err := bson.Marshal(doc)
	require.NoError(t, err)

	// A bson.Raw is copied value by value through the bounded writer.
	buf, bound, err = encodeBounded(t, bson.D{{Key: "raw", Value: bson.Raw(raw)}}, filtereval.MaxDocumentBytes)
	require.NoError(t, err)
	require.Equal(t, buf.Len(), bound.size)
}

func TestBoundedWriterRefusesPastLimit(t *testing.T) {
	t.Parallel()

	doc := bson.D{{Key: "v", Value: "0123456789"}}

	buf, _, err := encodeBounded(t, doc, filtereval.MaxDocumentBytes)
	require.NoError(t, err)

	_, _, err = encodeBounded(t, doc, buf.Len())
	require.NoError(t, err, "an encoding of exactly the limit is admitted")

	_, _, err = encodeBounded(t, doc, buf.Len()-1)
	require.ErrorIs(t, err, ErrLimitExceeded)
}

func TestBoundedWriterRefusesDeepNesting(t *testing.T) {
	t.Parallel()

	var deep any = int32(1)
	for range testNesting {
		deep = bson.D{{Key: "d", Value: deep}}
	}

	_, _, err := encodeBounded(t, bson.D{{Key: "v", Value: deep}}, filtereval.MaxDocumentBytes)
	require.ErrorIs(t, err, ErrLimitExceeded)
}
