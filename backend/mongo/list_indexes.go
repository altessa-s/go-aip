// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/altessa-s/go-mongox/indexes"

	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Indexes returns the compound index of every declared pattern, for the store's own index setup: keyed
// {scope keys…: 1, k1: d1, …, kn: dn, tie-breaker: dn} and named by [WithListIndexNames] or after its keys. Each serves its
// pattern and the pattern's reversal without a blocking sort (mongotest.AssertIndexedSorts checks it with explain). The
// unscoped id-only pattern uses the built-in [IDIndex] and is not returned.
func (l *ListEngine[D]) Indexes() []mongo.IndexModel {
	out := make([]mongo.IndexModel, len(l.indexes))
	for i, ix := range l.indexes {
		out[i] = mongo.IndexModel{Keys: slices.Clone(ix.keys), Options: mongooptions.Index().SetName(ix.name)}
	}

	return out
}

// EnsureIndexes creates, idempotently, the store's own indexes (extra) and the index of every sort pattern in one
// createIndexes command. An existing index with the same specification is kept; one that conflicts (the same name with other
// keys or options, or the same keys under another name) fails with the server's error and is never dropped: renaming an index
// is a migration, so keep an existing name with [WithListIndexNames] or drop the old index first. A model without a name gets
// the name the driver would give it; two models of one name fail before the command is sent.
func (l *ListEngine[D]) EnsureIndexes(ctx context.Context, extra ...mongo.IndexModel) error {
	return indexes.Ensure(ctx, l.coll, append(slices.Clone(extra), l.Indexes()...))
}

// indexName returns the name MongoDB generates for an index of keys: every key and its direction joined by "_".
func indexName(keys bson.D) string {
	parts := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		parts = append(parts, k.Key, fmt.Sprint(k.Value))
	}

	return strings.Join(parts, "_")
}
