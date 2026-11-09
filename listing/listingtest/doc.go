// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package listingtest provides the shared conformance suite and fixtures for listing engines.
//
// Run(t, factory, opts...) runs shared request validation, filter semantics, sort-pattern reversal, paging, presence, and token-binding cases.
// Factory receives schema, pagetoken.Signer, and fixture items, and returns a Lister over the backend. Expectations are computed independently of
// the engine.
//
// # Usage
//
//	listingtest.Run(t, func(t *testing.T, s *listing.Schema, codec pagetoken.Signer, items []listingtest.Widget) listingtest.Lister {
//		l, err := memory.NewListEngine(codec, s, listingtest.ID, listingtest.Values)
//		require.NoError(t, err)
//		return func(ctx context.Context, subject string, req listing.Request) (page.Page[listingtest.Widget], error) {
//			return l.List(ctx, subject, req, items)
//		}
//	})
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package listingtest
