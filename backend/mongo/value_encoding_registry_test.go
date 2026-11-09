// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// distinctStructs returns n values of struct types constructed at run time, unknown to any cache: each with a field a set to 1,
// a nil slice, a nil map and a nil pointer of an array type of its own, and a nil pointer to a struct type of its own it inlines,
// whose field has another array type of its own: the driver describes the inlined type, and looks up the type of its field,
// without a value of either.
func distinctStructs(n int) []any {
	prefix := "R" + strconv.FormatInt(time.Now().UnixNano(), 36) + "x"

	values := make([]any, n)
	for i := range values {
		own := reflect.ArrayOf(i+1, reflect.TypeFor[struct{}]())
		inlined := reflect.StructOf([]reflect.StructField{
			{Name: prefix + strconv.Itoa(i), Type: reflect.ArrayOf(i+1, reflect.TypeFor[[0]int]())},
		})
		t := reflect.StructOf([]reflect.StructField{
			{Name: "A", Type: reflect.TypeFor[int](), Tag: `bson:"a"`},
			{Name: prefix + strconv.Itoa(i), Type: reflect.SliceOf(own)},
			{Name: "M", Type: reflect.MapOf(reflect.TypeFor[string](), own)},
			{Name: "P", Type: reflect.PointerTo(own)},
			{Name: "I", Type: reflect.PointerTo(inlined), Tag: `bson:",inline"`},
		})
		v := reflect.New(t).Elem()
		v.Field(0).SetInt(1)
		values[i] = v.Interface()
	}

	return values
}

// heapAfterGC returns the bytes of the heap in use once the garbage collector has run.
func heapAfterGC() uint64 {
	runtime.GC()

	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)

	return stats.HeapAlloc
}

// TestEvaluator_PooledRegistriesBounded: review finding R15-1 — the registries that encode struct values through the driver are
// pooled, and the driver caches in a registry an encoder, a decoder and a description for every type it meets, so a program
// evaluating values of ever new types (reflect.StructOf, SliceOf, MapOf, ArrayOf) grew the caches of the registry it reused
// without bound, across garbage collections (verified on the unfixed code: for 20,000 new types, a pooled registry of 25,000 to
// 188,000 entries, and up to 88 MB retained after a garbage collection). A pooled registry caches at most
// [mongo.MaxRegistryUnits] entries besides those it is built with, every one of them counted; one that would cache more encodes
// its value and is dropped. The values are evaluated as before.
func TestEvaluator_PooledRegistriesBounded(t *testing.T) { //nolint:paralleltest // measures the pool and the heap
	const types = 20000

	node := filtertest.MustParseFilter(t, "v.a == 1")
	evaluate := func(values []any) {
		for mode, eval := range encodedEvaluators(t, filter.WithArrayFields("v.a")) {
			for _, v := range values {
				got, err := eval.Evaluate(node, map[string]any{"v": v})
				require.NoError(t, err, mode)
				require.True(t, got, mode)
			}
		}
	}

	evaluate(distinctStructs(types)) // fills the caches that are bounded anyway
	requirePoolBounded(t)

	values := distinctStructs(types)
	before := heapAfterGC()

	evaluate(values)

	after := heapAfterGC()
	retained := int64(after) - int64(before) //nolint:gosec // heap sizes fit an int64
	require.Less(t, retained, int64(12<<20), "retained %d bytes for %d types", retained, types)
	runtime.KeepAlive(values)

	requirePoolBounded(t)
}

// TestEvaluator_IdleRegistriesBounded: review finding R16-1 — the pool of encoding registries kept every registry released to
// it, so overlapping evaluations, each taking or building a registry, left as many in the pool as ran at the peak, and the
// caches they retain grew with the concurrency. The pool keeps at most [mongo.MaxPooledRegistries] idle registries, each
// within [mongo.MaxRegistryUnits] entries besides those it is built with; the others are dropped when released.
func TestEvaluator_IdleRegistriesBounded(t *testing.T) { //nolint:paralleltest // measures the pool
	const (
		held    = 4 * mongo.MaxPooledRegistries
		workers = 4 * mongo.MaxPooledRegistries
		each    = 200
	)
	mongo.HoldRegistries(held)() // more registries at once than the pool keeps, then all released
	requirePoolBounded(t)

	node := filtertest.MustParseFilter(t, "v.a == 1")

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for _, eval := range encodedEvaluators(t, filter.WithArrayFields("v.a")) {
				for _, v := range distinctStructs(each) {
					got, err := eval.Evaluate(node, map[string]any{"v": v})
					if err != nil || !got {
						t.Errorf("Evaluate = %v, %v; want true", got, err)
						return
					}
				}
			}
		})
	}

	wg.Wait()
	requirePoolBounded(t)
}

// requirePoolBounded checks that the pool keeps at most [mongo.MaxPooledRegistries] idle registries, each caching at most
// [mongo.MaxRegistryUnits] entries besides those it is built with, and none they do not count.
func requirePoolBounded(t *testing.T) {
	t.Helper()

	count, most, total, fresh, uncounted, err := mongo.PooledRegistries()
	require.NoError(t, err)
	require.LessOrEqual(t, count, mongo.MaxPooledRegistries)
	require.LessOrEqual(t, most, fresh+mongo.MaxRegistryUnits)
	require.LessOrEqual(t, total, mongo.MaxPooledRegistries*(fresh+mongo.MaxRegistryUnits))
	require.LessOrEqual(t, uncounted, 0)
}

// encodedEvaluators returns a default and a MongoDB-semantics evaluator reading values through the MongoDB value encoding.
func encodedEvaluators(t *testing.T, opts ...filter.PolicyOption) map[string]*filtereval.Evaluator {
	t.Helper()

	plain, err := newEvaluator(withPolicy(opts...), filtereval.WithValueEncoding(mongo.ValueEncoding()))
	require.NoError(t, err)

	semantic, err := newEvaluator(withPolicy(opts...), filtereval.WithValueEncoding(mongo.ValueEncoding()), filtereval.WithProfile(mongo.DefaultSchema()))
	require.NoError(t, err)

	return map[string]*filtereval.Evaluator{"default": plain, "mongo": semantic}
}
