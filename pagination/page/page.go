// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package page

// Page is one page of a list result (AIP-158).
type Page[T any] struct {
	// Items are the page's items in the list's order.
	Items []T
	// NextPageToken continues the list after the last item; empty on the
	// last page.
	NextPageToken string
}
