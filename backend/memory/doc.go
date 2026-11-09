// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package memory is the in-memory backend: it orders, projects and pages records held in memory with the semantics of every other
// backend, for small catalogs, code-defined collections, caches and test stores. In-memory filtering is the filter/evaluator
// package; this package builds on it.
//
// Three kinds share the package, each with its own constructor and option type:
//
//   - Order: NewOrderTranslator(policy, ...OrderOption) binds the resource's shared [orderby.Policy] to the storage names of the
//     records (WithOrderOptions) and [OrderTranslator.Translate] returns a [Comparator] over map[string]any records for
//     [slices.SortFunc] and [slices.SortStableFunc]. Values are read in their stored form (WithOrderValueEncoding adapts driver
//     types) and compare in the portable scalar order of pagetoken.SortValue: numbers exactly across integer, float and decimal
//     types, then strings byte-wise, then bools, then timestamps. Null sorts first by default (MongoDB order) or where the key's
//     nulls placement puts it; documents, arrays and bytes sort after every scalar. A collation fails with
//     [orderby.ErrUnsupportedOrdering].
//   - Projection: NewProjectionTranslator(policy, ...ProjectionOption) binds the shared [projection.Policy];
//     [ProjectionTranslator.Translate] and [ProjectionTranslator.TranslateView] return a [Projection] whose Apply projects a
//     map[string]any into a new map and whose ApplyProto filters or prunes a proto message in place, with the rules of the MongoDB
//     backend.
//   - List: NewListEngine(signer, schema, id, fields, ...ListEngineOption) returns a [ListEngine] that pages a slice with the
//     portable listing contract, typed keyset cursors verified before the scan and request work limits (WithListMaxWork,
//     on by default at [DefaultMaxWork], WithListMaxScanned, WithListMaxMaterialized; exhausted limits fail with [ErrWorkBudget]).
//     Every page scans the whole slice and keeps the page in a bounded heap (O(n log k) for page size k), so it is for small
//     collections only.
//
// Every option of a kind carries the kind's prefix: WithOrder…, WithProjection…, WithList….
//
// # Usage
//
//	compare, err := orderTranslator.Translate(spec) // from NewOrderTranslator
//	if err != nil {
//		return err
//	}
//	if compare != nil {
//		slices.SortStableFunc(records, compare)
//	}
//
//	proj, err := projectionTranslator.Translate(readMask) // from NewProjectionTranslator
//	if err != nil {
//		return err
//	}
//	out := proj.Apply(doc)
//
//	list, err := memory.NewListEngine(codec, schema, func(w Widget) string { return w.ID }, widgetValues)
//	if err != nil {
//		return err
//	}
//	if err := list.Validate(ctx, req); err != nil { // optional: before loading the items
//		return err
//	}
//	page, err := list.List(ctx, method, req, items) // items already restricted to the parent
//
// See README.md for the complete guide, limits, errors and extension contracts.
package memory
