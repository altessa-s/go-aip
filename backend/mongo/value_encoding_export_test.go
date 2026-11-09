// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"
)

// MaxRegistryUnits bounds the units of the driver's caches in a pooled encoding registry.
const MaxRegistryUnits = maxRegistryUnits

// MaxPooledRegistries bounds the idle encoding registries kept in the pool.
const MaxPooledRegistries = maxPooledRegistries

// PooledRegistries reports the idle encoding registries in the pool: their number, the most entries the driver caches in one of
// them (its type encoders and decoders, its struct descriptions and its pointer codecs' caches), the sum of those entries, the
// entries of a registry never used, and the most entries one of them caches beyond those it is built with and the units it
// counts. It reads the driver's unexported caches, so it is tied to the driver's version: a cache it does not find fails it,
// naming the cache. The registries return to the pool, unless concurrent encodings filled it meanwhile.
func PooledRegistries() (count, most, total, fresh, uncounted int, err error) {
	if fresh, err = registryEntries(newHookEncoder(registryTypes{})); err != nil {
		return 0, 0, 0, 0, 0, err
	}

	var taken []*hookEncoder
	defer func() {
		for _, h := range taken {
			h.release() // never blocks: dropped if concurrent encodings filled the pool meanwhile
		}
	}()

	for h := acquireHookEncoder(); h != nil; h = acquireHookEncoder() {
		taken = append(taken, h)

		entries, err := registryEntries(h)
		if err != nil {
			return 0, 0, 0, 0, 0, err
		}

		most = max(most, entries)
		total += entries
		uncounted = max(uncounted, entries-fresh-h.types.units)
	}

	return len(taken), most, total, fresh, uncounted, nil
}

// HoldRegistries takes n encoding registries at once, from the pool or new ones, as n concurrent encodings would, and returns the
// function releasing all of them.
func HoldRegistries(n int) (release func()) {
	held := make([]*hookEncoder, n)
	for i := range held {
		if held[i] = acquireHookEncoder(); held[i] == nil {
			held[i] = newHookEncoder(registryTypes{})
		}
	}

	return func() {
		for _, h := range held {
			h.release()
		}
	}
}

// registryEntries counts the entries of the caches of h's registry.
func registryEntries(h *hookEncoder) (int, error) {
	r := reflect.ValueOf(h.registry).Elem()
	n := 0

	for _, path := range [][]string{{"typeEncoders", "cache"}, {"typeDecoders", "cache"}} {
		m, err := driverField(r, path...)
		if err != nil {
			return 0, err
		}

		n += syncMapLen(m)
	}

	caches := []struct {
		kinds string
		kind  reflect.Kind
		maps  [][]string
	}{
		{kinds: "kindEncoders", kind: reflect.Struct, maps: [][]string{{"cache"}}},
		{kinds: "kindEncoders", kind: reflect.Pointer, maps: [][]string{{"ecache", "cache"}, {"dcache", "cache"}}},
		{kinds: "kindDecoders", kind: reflect.Pointer, maps: [][]string{{"ecache", "cache"}, {"dcache", "cache"}}},
	}
	for _, c := range caches {
		codec, err := kindCodec(r, c.kinds, c.kind)
		if err != nil {
			return 0, err
		}

		for _, path := range c.maps {
			m, err := driverField(codec, path...)
			if err != nil {
				return 0, err
			}

			n += syncMapLen(m)
		}
	}

	return n, nil
}

// kindCodec returns the struct of the codec the registry r keeps for kind in its kinds cache.
func kindCodec(r reflect.Value, kinds string, kind reflect.Kind) (reflect.Value, error) {
	entries, err := driverField(r, kinds, "entries")
	if err != nil {
		return reflect.Value{}, err
	}

	slot := (*atomic.Value)(unsafe.Pointer(entries.Index(int(kind)).UnsafeAddr())).Load() //nolint:gosec // reads the driver's cache
	if slot == nil {
		return reflect.Value{}, fmt.Errorf("driver internals changed: no %s codec for %s", kinds, kind)
	}

	codec := reflect.ValueOf(slot).Elem().Field(0).Elem()
	if codec.Kind() != reflect.Pointer || codec.Elem().Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("driver internals changed: %s codec for %s is %s", kinds, kind, codec.Type())
	}

	return codec.Elem(), nil
}

// driverField follows the field names path from v, through pointers, and fails naming the first field not found.
func driverField(v reflect.Value, path ...string) (reflect.Value, error) {
	for _, name := range path {
		if v.Kind() == reflect.Pointer {
			v = v.Elem()
		}

		if v.Kind() != reflect.Struct {
			return reflect.Value{}, fmt.Errorf("driver internals changed: %s is not a struct holding %q", v.Type(), name)
		}

		f := v.FieldByName(name)
		if !f.IsValid() {
			return reflect.Value{}, fmt.Errorf("driver internals changed: %s has no field %q", v.Type(), name)
		}

		v = f
	}

	if v.Type() != reflect.TypeFor[sync.Map]() && v.Kind() != reflect.Array {
		return reflect.Value{}, fmt.Errorf("driver internals changed: %v is a %s", path, v.Type())
	}

	return v, nil
}

// syncMapLen counts the entries of the sync.Map v.
func syncMapLen(v reflect.Value) int {
	m := (*sync.Map)(unsafe.Pointer(v.UnsafeAddr())) //nolint:gosec // reads the driver's cache in a test
	n := 0

	m.Range(func(any, any) bool {
		n++
		return true
	})

	return n
}
