// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package keyset

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// mustCanonical returns the canonical form of filter.
func mustCanonical(tb testing.TB, filter bson.D) bson.D {
	tb.Helper()

	d, err := canonicalFilter(tb.Context(), filter)
	require.NoError(tb, err)

	return d
}

// mapFilter returns a filter holding multi-entry maps at every position a filter admits one.
func mapFilter() bson.D {
	return bson.D{
		{Key: "score", Value: bson.M{"$gte": 1, "$lt": 10, "$ne": 5, "$exists": true}},
		{Key: "$or", Value: []bson.M{{"a": 1, "b": 2, "c": 3}, {"d": map[string]any{"x": 1, "y": 2, "z": 3}}}},
		{Key: "tags", Value: bson.M{"$in": []map[string]any{{"p": 1, "q": 2, "r": 3}}, "$nin": bson.A{"x"}}},
		{Key: "$and", Value: bson.A{bson.D{{Key: "m", Value: map[string]any{"k1": 1, "k2": 2, "k3": 3}}}}},
		{Key: "nested", Value: [][]any{{bson.M{"u": 1, "v": 2, "w": 3}}}},
	}
}

// TestCanonicalFilterIsDeterministic checks a filter of multi-entry maps encodes to the same bytes, and binds the same token,
// every time.
func TestCanonicalFilterIsDeterministic(t *testing.T) {
	t.Parallel()

	keys := []sortKey{{field: "_id"}}

	var want pagetokenBindings

	for i := range 200 {
		filter := mapFilter()
		require.NoError(t, ValidateFilter(t.Context(), filter, nil))

		raw, err := marshalFilter(mustCanonical(t, filter), nil, nil)
		require.NoError(t, err)

		b, err := newBindings(raw, keys, "subject")
		require.NoError(t, err)

		if i == 0 {
			want = pagetokenBindings{filter: b.Filter, raw: raw}

			continue
		}

		require.Equal(t, want.filter, b.Filter, "run %d", i)
		require.Equal(t, want.raw, raw, "run %d", i)
	}
}

// pagetokenBindings is what one run of TestCanonicalFilterIsDeterministic binds.
type pagetokenBindings struct {
	filter string
	raw    bson.Raw
}

// TestCanonicalFilterKeepsMeaning checks the canonical filter holds the same documents as the original, sorts map entries,
// keeps bson.D order and leaves the original unchanged.
func TestCanonicalFilterKeepsMeaning(t *testing.T) {
	t.Parallel()

	filter := mapFilter()
	canonical := mustCanonical(t, filter)

	require.Equal(t, asMaps(t, filter), asMaps(t, canonical))

	require.Equal(t, bson.D{{Key: "$exists", Value: true}, {Key: "$gte", Value: 1}, {Key: "$lt", Value: 10}, {Key: "$ne", Value: 5}},
		canonical[0].Value)
	require.Equal(t, []string{"score", "$or", "tags", "$and", "nested"}, keysOf(canonical))
	require.IsType(t, bson.M{}, filter[0].Value, "the original is not modified")
}

// asMaps returns the documents of d decoded as maps at every depth: d without its key order.
func asMaps(tb testing.TB, d bson.D) bson.M {
	tb.Helper()

	raw, err := bson.Marshal(d)
	require.NoError(tb, err)

	dec := bson.NewDecoder(bson.NewDocumentReader(bytes.NewReader(raw)))
	dec.DefaultDocumentM()

	var out bson.M
	require.NoError(tb, dec.Decode(&out))

	return out
}

// keysOf returns the keys of d in order.
func keysOf(d bson.D) []string {
	out := make([]string, len(d))
	for i, e := range d {
		out[i] = e.Key
	}

	return out
}

// TestCanonicalFilterSharesOrderedValues checks a filter without a multi-entry map is returned as it is.
func TestCanonicalFilterSharesOrderedValues(t *testing.T) {
	t.Parallel()

	filter := bson.D{
		{Key: "a", Value: bson.M{"$gt": 1}},
		{Key: "b", Value: bson.D{{Key: "z", Value: 1}, {Key: "a", Value: 2}}},
		{Key: "c", Value: []string{"x", "y"}},
		{Key: "d", Value: bson.A{map[string]any{"k": 1}}},
	}

	got := mustCanonical(t, filter)
	require.Equal(t, filter, got)
	require.Same(t, &filter[0], &got[0])
}
