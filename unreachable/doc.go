// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package unreachable implements the unreachable resources of AIP-217 for reads that fan out across sources: Lists across collections
// (AIP-159), locations, shards or cells.
//
// A Set collects the service-relative resource names of the unreachable resources of one response, deduplicated, sorted for a
// deterministic response (AIP-217 gives the order no meaning) and bounded by WithMaxNames after an optional WithUpscope. Set.Response
// applies return_partial_success: true returns the names for the unreachable field, false fails the request with an *Error that maps to
// UNAVAILABLE. CheckGranularity rejects return_partial_success on a request scope the API cannot report on (INVALID_ARGUMENT).
//
// Run is a generic fan-out of one page: it fetches every Source with bounded concurrency and a per-source timeout, merges the items in
// source order and reports the sources whose errors the classifier accepts (UNAVAILABLE and deadline errors by default) as unreachable.
// Any other error fails the run, and so does an unreachable Required source, the single parent a request is scoped to. LintMethod checks
// the request and response descriptors of a method against the AIP-217 field rules.
//
// # Usage
//
//	if err := unreachable.CheckGranularity(req.GetReturnPartialSuccess(), parent.IsWildcard()); err != nil {
//		return nil, err
//	}
//	res, err := unreachable.Run(ctx, sources, unreachable.WithPartialSuccess(req.GetReturnPartialSuccess()))
//	if err != nil {
//		return nil, err
//	}
//	return &pb.ListBooksResponse{Books: res.Items, Unreachable: res.Unreachable}, nil
//
// See README.md for the complete guide, the AIP-217 conformance table, errors and limits.
package unreachable
