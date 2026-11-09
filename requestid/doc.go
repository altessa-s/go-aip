// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package requestid implements AIP-155 request identification: it validates
// the optional request_id of a request message and deduplicates requests by
// it, so that a client retry with the same ID never runs a method twice.
//
// A [Guard] wraps a handler with [Do]. The first request with an ID reserves
// it in a [Store] and runs the handler; its response is stored, serialized,
// with a fingerprint of the request parameters (a hash of the deterministic
// encoding of the request without its request ID and the fields of
// [WithIgnoredFields]). A duplicate with the same fingerprint gets the stored
// response without running the handler; a duplicate of a request still
// running waits for it; the same ID with other parameters is
// [ErrRequestIDReused]. IDs are scoped by method and principal ([Scope]), so
// IDs of different callers never collide. An empty request ID disables
// deduplication.
//
// [Store] is the storage contract; [MemoryStore] is its in-memory reference
// implementation and the requestidtest package its conformance suite for
// other adapters.
//
// # Usage
//
//	guard, err := requestid.New(store, requestid.WithFormat(requestid.FormatUUID4))
//	if err != nil {
//		return err
//	}
//
//	func (s *server) CreateBook(ctx context.Context, req *pb.CreateBookRequest) (*pb.Book, error) {
//		scope := requestid.Scope{Method: pb.LibraryService_CreateBook_FullMethodName, Principal: principalOf(ctx)}
//		return requestid.Do(ctx, s.guard, scope, req, s.createBook)
//	}
//
// See README.md for the complete guide, the AIP-155 conformance table, errors
// and limits.
package requestid
