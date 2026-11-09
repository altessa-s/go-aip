// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package keyset

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// prefixedMap returns a two-entry map whose keys share a prefix of n bytes.
func prefixedMap(n int) map[string]any {
	prefix := strings.Repeat("k", n)

	return map[string]any{prefix + "a": int32(1), prefix + "b": int32(2)}
}

// TestValidateFilterBoundsSharedKeyBytes checks a filter referencing one map of long keys many times, small in memory and in
// values but huge in the key bytes its encoding and its canonicalization would go through, fails fast in the walk and in
// FindPage, before either compares a key.
func TestValidateFilterBoundsSharedKeyBytes(t *testing.T) {
	t.Parallel()

	m := prefixedMap(1 << 20)

	refs := make(bson.A, 1<<16)
	for i := range refs {
		refs[i] = m
	}

	filter := bson.D{{Key: "x", Value: refs}}
	start := time.Now()

	require.ErrorIs(t, ValidateFilter(t.Context(), filter, nil), errFilterTooLarge)

	_, err := FindPage[bson.M](t.Context(), nil, nil, Query{Filter: filter})
	require.ErrorIs(t, err, errFilterTooLarge)

	requireQuick(t, time.Since(start), fastFail)
}

// TestValidateFilterCountsVariableBytes checks every key and every variable-size leaf counts against the byte budget, as often as
// the encoding repeats it, exactly at the limit.
func TestValidateFilterCountsVariableBytes(t *testing.T) {
	t.Parallel()

	const fits = maxFilterBytes - 1 // the key "x" of the filter document

	half := func(n int) string { return strings.Repeat("v", n/2) }
	raw := func(n int) bson.Raw {
		r := rawOf(t, bson.D{{Key: "s", Value: strings.Repeat("r", n-13)}}) // the document, the key and the string framing
		require.Len(t, r, n)

		return r
	}

	for name, sized := range map[string]func(n int) any{
		"string":        func(n int) any { return strings.Repeat("v", n) },
		"bytes":         func(n int) any { return make([]byte, n) },
		"binary":        func(n int) any { return bson.Binary{Data: make([]byte, n)} },
		"regex":         func(n int) any { return bson.Regex{Pattern: strings.Repeat("p", n-1), Options: "i"} },
		"key":           func(n int) any { return bson.D{{Key: strings.Repeat("k", n), Value: int32(1)}} },
		"map key":       func(n int) any { return bson.M{strings.Repeat("k", n): int32(1)} },
		"string list":   func(n int) any { return []string{half(n), half(n + 1)} },
		"shared string": func(n int) any { s := half(n); return bson.A{s, s + strings.Repeat("v", n%2)} },
		"raw":           func(n int) any { return raw(n) },
	} {
		require.NoError(t, ValidateFilter(t.Context(), bson.D{{Key: "x", Value: sized(fits)}}, nil), name)
		require.ErrorIs(t, ValidateFilter(t.Context(), bson.D{{Key: "x", Value: sized(fits + 1)}}, nil), errFilterTooLarge, name)
	}

	shared := strings.Repeat("v", fits/2+1)
	require.ErrorIs(t, ValidateFilter(t.Context(), bson.D{{Key: "x", Value: bson.A{shared, shared}}}, nil), errFilterTooLarge,
		"a shared string counts at every reference")
}

// TestCanonicalFilterCanonicalizesSharedMapsOnce checks a map reached through many references is canonicalized once: every
// reference gets the same canonical document, and a filter within the budget whose shared map has long keys canonicalizes fast.
func TestCanonicalFilterCanonicalizesSharedMapsOnce(t *testing.T) {
	t.Parallel()

	m := prefixedMap(1 << 16)

	refs := make(bson.A, 100)
	for i := range refs {
		refs[i] = m
	}

	filter := bson.D{{Key: "x", Value: refs}, {Key: "y", Value: []map[string]any{m}}}
	require.NoError(t, ValidateFilter(t.Context(), filter, nil))

	start := time.Now()
	canonical := mustCanonical(t, filter)

	requireQuick(t, time.Since(start), fastFail)

	list, ok := canonical[0].Value.(bson.A)
	require.True(t, ok)

	first, ok := list[0].(bson.D)
	require.True(t, ok)
	require.Len(t, first, 2)

	for _, v := range list {
		d, ok := v.(bson.D)
		require.True(t, ok)
		require.Same(t, &first[0], &d[0], "every reference shares one canonical document")
	}

	requireSameBSON(t, filter, canonical)
}

// TestCanonicalFilterStopsOnCancel checks canonicalization returns the context's error once its context ends, and completes a
// pass shorter than one check interval.
func TestCanonicalFilterStopsOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	maps := make([]bson.M, 4*cancelCheckInterval)
	for i := range maps {
		maps[i] = bson.M{"a": int32(i), "b": int32(i)}
	}

	_, err := canonicalFilter(ctx, bson.D{{Key: "x", Value: maps}})
	require.ErrorIs(t, err, context.Canceled)

	_, err = FindPage[bson.M](ctx, nil, nil, Query{Filter: bson.D{{Key: "x", Value: maps}}})
	require.ErrorIs(t, err, context.Canceled)

	got, err := canonicalFilter(ctx, bson.D{{Key: "x", Value: bson.M{"b": 1, "a": 2}}})
	require.NoError(t, err, "a pass shorter than one interval completes")
	require.Equal(t, bson.D{{Key: "x", Value: bson.D{{Key: "a", Value: 2}, {Key: "b", Value: 1}}}}, got)
}
