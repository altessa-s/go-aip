// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"maps"
	"slices"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// orderedDocument converts a translated document to a bson.D with sorted keys, converting nested documents and lists too.
func orderedDocument(m bson.M) bson.D {
	keys := slices.Sorted(maps.Keys(m))

	out := make(bson.D, len(keys))
	for i, k := range keys {
		out[i] = bson.E{Key: k, Value: orderedValue(m[k])}
	}

	return out
}

// orderedValue converts the documents inside a translated value; other values are returned as they are.
func orderedValue(v any) any {
	switch x := v.(type) {
	case bson.M:
		return orderedDocument(x)
	case bson.A:
		return orderedList(x)
	case []any:
		return orderedList(x)
	default:
		return v
	}
}

// orderedList converts the documents among the elements of a translated list.
func orderedList[S ~[]any](list S) bson.A {
	out := make(bson.A, len(list))
	for i, v := range list {
		out[i] = orderedValue(v)
	}

	return out
}
