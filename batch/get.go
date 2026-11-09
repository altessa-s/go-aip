// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package batch

import "context"

// FetchFunc reads the resources of keys, all distinct, at one consistent
// point in time (a read transaction or a single query) and returns those
// that exist by key. A key missing from the map does not exist.
type FetchFunc[K comparable, R any] func(ctx context.Context, keys []K) (map[K]R, error)

// Get runs an AIP-231 Batch Get of nested requests: keyOf returns the
// resource name or id an item addresses, fetch reads the distinct keys in
// one call, and the resources come back in the order of the items, a
// resource repeated as often as its key is (AIP-231: the order of the
// response must be the order of the request).
//
// Batch Get is atomic: the first item whose resource does not exist fails
// the batch with the *[ItemError] of its index wrapping [ErrNotFound]
// (NOT_FOUND), and no resources are returned. An error of fetch is returned
// as is. field is the JSON name of the repeated field.
func Get[T any, K comparable, R any](ctx context.Context, field string, items []T, keyOf func(item T) K, fetch FetchFunc[K, R]) ([]R, error) {
	keys := make([]K, len(items))
	for i, item := range items {
		keys[i] = keyOf(item)
	}

	return GetKeys(ctx, field, keys, fetch)
}

// GetKeys is [Get] for the "names" form of AIP-231, the items being the
// keys themselves.
func GetKeys[K comparable, R any](ctx context.Context, field string, keys []K, fetch FetchFunc[K, R]) ([]R, error) {
	distinct := make([]K, 0, len(keys))
	seen := make(map[K]struct{}, len(keys))

	for _, k := range keys {
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			distinct = append(distinct, k)
		}
	}

	found, err := fetch(ctx, distinct)
	if err != nil {
		return nil, err
	}

	out := make([]R, len(keys))

	for i, k := range keys {
		r, ok := found[k]
		if !ok {
			return nil, NewItemError(field, i, ErrNotFound)
		}

		out[i] = r
	}

	return out, nil
}
