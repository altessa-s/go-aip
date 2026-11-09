// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package bsonbound_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/backend/mongo/internal/bsonbound"
)

// units is a budget of n units.
type units struct{ n int }

func (u *units) Charge(n int) bool {
	if n > u.n {
		u.n = 0
		return false
	}

	u.n -= n

	return true
}

func TestMarshal(t *testing.T) {
	t.Parallel()

	doc := bson.D{{Key: "a", Value: bson.A{int32(1), "two", bson.D{{Key: "b", Value: true}}}}}

	want, err := bson.Marshal(doc)
	require.NoError(t, err)

	raw, err := bsonbound.Marshal(doc, len(want), 3, nil)
	require.NoError(t, err)
	require.Equal(t, bson.Raw(want), raw)

	_, err = bsonbound.Marshal(doc, len(want)-1, 3, nil)
	require.ErrorIs(t, err, bsonbound.ErrLimitExceeded, "one byte short")

	_, err = bsonbound.Marshal(doc, len(want), 2, nil)
	require.ErrorIs(t, err, bsonbound.ErrLimitExceeded, "one level short")

	configured := false
	_, err = bsonbound.Marshal(doc, len(want), 3, func(enc *bson.Encoder) {
		configured = true

		enc.SetRegistry(bson.NewRegistry())
	})
	require.NoError(t, err)
	require.True(t, configured)
}

// TestMarshalStopsSharedGraphs checks a small value sharing its parts fails at the limit instead of expanding.
func TestMarshalStopsSharedGraphs(t *testing.T) {
	t.Parallel()

	var v any = bson.A{int32(1)}
	for range 40 {
		v = bson.A{v, v}
	}

	_, err := bsonbound.Marshal(bson.D{{Key: "v", Value: v}}, 1<<16, 64, nil)
	require.ErrorIs(t, err, bsonbound.ErrLimitExceeded)
}

// TestNewValueWriterChargesBudget checks the encoding is charged to its budget as it grows and fails once the budget is spent.
func TestNewValueWriterChargesBudget(t *testing.T) {
	t.Parallel()

	doc := bson.D{{Key: "s", Value: string(make([]byte, 1024))}}

	var buf bytes.Buffer

	budget := &units{n: 1 << 20}
	vw, bound := bsonbound.NewValueWriter(bson.NewDocumentWriter(&buf), 1<<20, 4, budget)
	require.NoError(t, bson.NewEncoder(vw).Encode(doc))
	require.Equal(t, buf.Len(), bound.Size())
	require.Zero(t, bound.Depth())
	require.Less(t, budget.n, 1<<20, "the encoding was charged")

	buf.Reset()

	vw, _ = bsonbound.NewValueWriter(bson.NewDocumentWriter(&buf), 1<<20, 4, &units{n: 1})
	require.ErrorIs(t, bson.NewEncoder(vw).Encode(doc), bsonbound.ErrLimitExceeded)
}
