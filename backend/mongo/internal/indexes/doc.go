// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package indexes creates the indexes of a MongoDB collection idempotently, naming an unnamed model the way the driver does. It
// is the index setup of the MongoDB list engine (mongo.ListEngine.EnsureIndexes), ported from go-atlas store/mongo/indexes
// without its drop-and-recreate reconciliation.
//
// # Usage
//
//	err := indexes.Ensure(ctx, coll, models)
package indexes
