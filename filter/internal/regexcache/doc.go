// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package regexcache provides isolated bounded caches of compiled RE2 patterns.
//
// This is an internal implementation package. Its exported helpers are available only to packages permitted by Go internal import rules.
// Mutable work state belongs to the caller or an explicitly constructed instance; consumers should use the public parent APIs.
//
// # Usage
//
//	cache, err := regexcache.New(256)
//	if err != nil { return err }
//	pattern, err := cache.Compile("^A.*e$")
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package regexcache
