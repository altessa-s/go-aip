// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package requestidtest is the conformance suite of the requestid.Store
// contract. A store adapter runs it from its own tests, so every store
// reserves, completes, releases and expires entries exactly as the in-memory
// reference store does.
//
// # Usage
//
//	func TestStoreConformance(t *testing.T) {
//		requestidtest.Run(t, func(t *testing.T) requestid.Store {
//			return newStoreOnFreshCollection(t)
//		})
//	}
//
// See README.md for the scenarios.
package requestidtest
