// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package keyset

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/backend/mongo/internal/bsonbound"
)

// fastFail is the most a walk over a compact graph may take before it fails: far below the time an expanded walk or encoding
// would need, far above a bounded one.
const fastFail = time.Second

// sharedGraphs returns small values whose encoding repeats their shared parts 2^levels times, one per container kind.
func sharedGraphs(levels int) map[string]any {
	var (
		arr   any = bson.A{int32(1)}
		list  any = []any{int32(1)}
		doc   any = map[string]any{"v": int32(1)}
		m     any = bson.M{"v": int32(1)}
		elems any = bson.D{{Key: "v", Value: int32(1)}}
		docs      = []bson.M{{"v": int32(1)}}
	)

	for range levels {
		arr = bson.A{arr, arr}
		list = []any{list, list}
		doc = map[string]any{"a": doc, "b": doc}
		m = bson.M{"a": m, "b": m}
		elems = bson.D{{Key: "a", Value: elems}, {Key: "b", Value: elems}}
		docs = []bson.M{{"a": docs, "b": docs}}
	}

	return map[string]any{"bson.A": arr, "[]any": list, "map": doc, "bson.M": m, "bson.D": elems, "[]bson.M": docs}
}

// TestValidateFilterRejectsSharedGraphs checks a compact graph sharing its parts, whose expansion holds far more values than
// maxFilterNodes, fails fast in the walk and in FindPage instead of being expanded: the walk stops once it has spent its
// maxFilterNodes visits, with bytes left, so it counted the shared parts at every reference and went no further.
func TestValidateFilterRejectsSharedGraphs(t *testing.T) {
	t.Parallel()

	for name, value := range sharedGraphs(40) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			filter := bson.D{{Key: "x", Value: value}}
			start := time.Now()

			w := newFilterWalk(t.Context(), nil)
			require.ErrorIs(t, w.value(filter, 0), errFilterTooLarge)
			require.Zero(t, w.left, "the visit budget stops the walk")
			require.Positive(t, w.bytesLeft, "the byte budget does not")

			require.ErrorIs(t, ValidateFilter(t.Context(), filter, nil), errFilterTooLarge)

			_, err := FindPage[bson.M](t.Context(), nil, nil, Query{Filter: filter})
			require.ErrorIs(t, err, errFilterTooLarge)

			requireQuick(t, time.Since(start), raceSlowdown*fastFail)
		})
	}
}

// TestMarshalFilterBoundsSharedGraphs checks the encoding of a compact graph that skipped the walk stops at the byte limit
// instead of building its expansion in memory.
func TestMarshalFilterBoundsSharedGraphs(t *testing.T) {
	t.Parallel()

	_, err := marshalFilter(bson.D{{Key: "x", Value: sharedGraphs(40)["bson.A"]}}, nil, nil)
	require.ErrorIs(t, err, bsonbound.ErrLimitExceeded)
}

// TestValidateFilterAcceptsSmallSharedGraphs checks a shared graph whose expansion stays within the limits passes every check
// and encodes to as many bytes as the driver encodes it to (a map has no key order).
func TestValidateFilterAcceptsSmallSharedGraphs(t *testing.T) {
	t.Parallel()

	for name, value := range sharedGraphs(8) {
		filter := bson.D{{Key: "x", Value: value}}

		require.NoError(t, ValidateFilter(t.Context(), filter, nil), name)

		raw, err := marshalFilter(filter, nil, nil)
		require.NoError(t, err, name)

		want, err := bson.Marshal(filter)
		require.NoError(t, err, name)
		require.Len(t, raw, len(want), name)
	}
}

// TestValidateFilterCountsExpandedValues checks the visit budget counts a value each time the encoding repeats it, exactly at
// the limit.
func TestValidateFilterCountsExpandedValues(t *testing.T) {
	t.Parallel()

	flat := make(bson.A, maxFilterNodes-2) // the filter document, the array and its elements
	for i := range flat {
		flat[i] = int32(i)
	}

	require.NoError(t, ValidateFilter(t.Context(), bson.D{{Key: "x", Value: flat}}, nil))
	require.ErrorIs(t, ValidateFilter(t.Context(), bson.D{{Key: "x", Value: append(flat, int32(0))}}, nil), errFilterTooLarge)
}

// TestValidateFilterChargesEveryElement checks every element of every container kind counts against the visit budget, exactly at
// the limit: empty documents of a []bson.M, the leaves of a typed slice and the elements of raw BSON.
func TestValidateFilterChargesEveryElement(t *testing.T) {
	t.Parallel()

	const fits = maxFilterNodes - 2 // the filter document, the container and its elements

	rawArray := func(n int) bson.RawArray {
		return bson.RawArray(rawOf(t, bson.D{{Key: "a", Value: make(bson.A, n)}}).Lookup("a").Value)
	}

	for name, sized := range map[string]func(n int) any{
		"[]bson.M":      func(n int) any { return make([]bson.M, n) },
		"[]bson.D":      func(n int) any { return make([]bson.D, n) },
		"[]int32":       func(n int) any { return make([]int32, n) },
		"[]any":         func(n int) any { return make([]any, n) },
		"bson.D":        func(n int) any { return make(bson.D, n) },
		"bson.RawArray": func(n int) any { return rawArray(n) },
	} {
		require.NoError(t, ValidateFilter(t.Context(), bson.D{{Key: "x", Value: sized(fits)}}, nil), name)
		require.ErrorIs(t, ValidateFilter(t.Context(), bson.D{{Key: "x", Value: sized(fits + 1)}}, nil), errFilterTooLarge, name)
	}
}

// TestValidateFilterStopsOnCancel checks the walk returns the context's error once its context ends, for every container kind and
// for raw BSON.
func TestValidateFilterStopsOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	const n = 4 * cancelCheckInterval

	for name, value := range map[string]any{
		"bson.A":   make(bson.A, n),
		"[]bson.M": make([]bson.M, n),
		"[]int64":  make([]int64, n),
		"bson.Raw": rawOf(t, bson.D{{Key: "a", Value: make(bson.A, n)}}),
	} {
		require.ErrorIs(t, ValidateFilter(ctx, bson.D{{Key: "x", Value: value}}, nil), context.Canceled, name)
	}

	require.NoError(t, ValidateFilter(ctx, bson.D{{Key: "x", Value: int32(1)}}, nil), "a walk shorter than one interval completes")
}

// TestValidateFilterBoundsRawBeforeParsing checks raw BSON beyond the document limit fails as too large before any byte of it is
// read: the zero bytes below are not well formed, yet the size fails first.
func TestValidateFilterBoundsRawBeforeParsing(t *testing.T) {
	t.Parallel()

	huge := make([]byte, maxFilterBytes+1)

	for name, value := range map[string]any{
		"bson.Raw":         bson.Raw(huge),
		"bson.RawArray":    bson.RawArray(huge),
		"raw document":     bson.RawValue{Type: bson.TypeEmbeddedDocument, Value: huge},
		"raw string":       bson.RawValue{Type: bson.TypeString, Value: huge},
		"slice of raw":     []bson.Raw{huge},
		"raw in a list":    bson.A{bson.Raw(huge)},
		"raw in an object": bson.M{"x": bson.Raw(huge)},
	} {
		start := time.Now()

		require.ErrorIs(t, ValidateFilter(t.Context(), value, nil), errFilterTooLarge, name)
		requireQuick(t, time.Since(start), fastFail, name)
	}
}

// expander is a filter value its registry codec encodes to a document of n strings of 1 KiB: a small value whose encoding is
// large.
type expander struct{ n int }

// TestMarshalFilterBoundsCustomEncodings checks a codec that expands a small value fails at the byte limit while it encodes.
func TestMarshalFilterBoundsCustomEncodings(t *testing.T) {
	t.Parallel()

	chunk := string(make([]byte, 1024))
	reg := bson.NewRegistry()
	reg.RegisterTypeEncoder(reflect.TypeFor[expander](), bson.ValueEncoderFunc(
		func(_ bson.EncodeContext, vw bson.ValueWriter, v reflect.Value) error {
			aw, err := vw.WriteArray()
			if err != nil {
				return err
			}

			for range v.Interface().(expander).n { //nolint:errcheck // the codec is registered for expander only
				ew, err := aw.WriteArrayElement()
				if err != nil {
					return err
				}

				if err := ew.WriteString(chunk); err != nil {
					return err
				}
			}

			return aw.WriteArrayEnd()
		}))

	_, err := marshalFilter(bson.D{{Key: "x", Value: expander{n: 8}}}, reg, nil)
	require.NoError(t, err)

	start := time.Now()
	_, err = marshalFilter(bson.D{{Key: "x", Value: expander{n: 1 << 20}}}, reg, nil)
	require.ErrorIs(t, err, bsonbound.ErrLimitExceeded)
	requireQuick(t, time.Since(start), fastFail)
}
