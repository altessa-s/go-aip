// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package page defines Page, one page of a list result: its items and the token of the next page (AIP-158).
//
// Page[T] contains Items in list order and NextPageToken. An empty next token marks the last page. The package has no dependencies; domain
// APIs and all listing engines can share this result shape without importing a database driver.
//
// # Usage
//
//	result := page.Page[string]{Items: []string{"example"}}
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package page
