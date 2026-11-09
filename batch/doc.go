// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package batch provides the request checks and execution helpers of the
// AIP batch methods: Batch Get (AIP-231), Batch Create (AIP-233), Batch
// Update (AIP-234) and Batch Delete (AIP-235).
//
// The checks run before any work, cheapest first: CheckSize bounds the item
// count (WithMaxItems, 1000 by default) and rejects an empty batch,
// CheckParent matches the parent of every item with the parent of the batch
// request ("-" segments as wildcards), CheckHoisted does the same for any
// other hoisted field, CheckUnique rejects repeated keys and Each applies the
// validation of the singular method to every nested request. They work on
// any item type through accessor functions, so they serve resource names and
// bare ids alike; ValidateRequest runs them on a message of the standard
// shape ("parent", "requests" or "names") by its descriptor.
//
// Execution keeps the response in request order. Atomic runs a batch all or
// nothing inside a caller's transaction, as every synchronous batch method
// must be; Get reads the resources of a Batch Get in one call and fails it
// with NOT_FOUND when any is missing; Partial runs the partial-success
// batches AIP-233 to AIP-235 allow for long-running operations, with bounded
// concurrency, and returns the failed requests by index.
//
// Every item failure is an *ItemError on its request path ("requests[2]",
// "names[0]"); a field error of a nested request is reported under that path
// ("requests[2].book.title") as a coreerrs.FieldError.
//
// # Usage
//
//	if err := batch.ValidateRequest(req, batch.WithUniqueKeys(true)); err != nil {
//		return nil, err
//	}
//	if err := batch.Each(batch.FieldRequests, req.GetRequests(), func(_ int, r *pb.UpdateBookRequest) error {
//		return validateUpdate(r)
//	}); err != nil {
//		return nil, err
//	}
//	books, err := batch.Atomic(ctx, batch.FieldRequests, req.GetRequests(), store.InTx,
//		func(ctx context.Context, _ int, r *pb.UpdateBookRequest) (*pb.Book, error) {
//			return svc.update(ctx, r)
//		})
//
//	books, err := batch.GetKeys(ctx, batch.FieldNames, req.GetNames(), store.GetMany)
//
// See README.md for the complete guide, the AIP conformance table and the
// errors.
package batch
