// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory_test

import (
	"testing"

	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

func BenchmarkList(b *testing.B) {
	ws := listingtest.MakeWidgets(500)
	l := newWidgets(b, listingtest.NewSchema(b), pagetokentest.Codec(b))

	req := listing.Request{PageSize: 50, Filter: `kind in ["KIND_A", "KIND_B"] && priority > 1`, OrderBy: "kind, priority desc"}
	for b.Loop() {
		if _, err := l.List(b.Context(), listingtest.Subject, req, ws); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkListPages lists the first and a later page of collections of several sizes, with and without a filter.
func BenchmarkListPages(b *testing.B) {
	const filtered = `kind in ["KIND_A", "KIND_B"] && priority > 1`

	cases := []struct {
		name  string
		items int
		req   listing.Request
	}{
		{name: "NoFilter500", items: 500, req: listing.Request{PageSize: 50, OrderBy: "name"}},
		{name: "NoFilter5000", items: 5000, req: listing.Request{PageSize: 50, OrderBy: "name"}},
		{name: "Filter5000", items: 5000, req: listing.Request{PageSize: 50, Filter: filtered, OrderBy: "kind, priority desc"}},
		{name: "Empty", items: 0, req: listing.Request{PageSize: 50, Filter: filtered, OrderBy: "kind, priority desc"}},
	}

	for _, tc := range cases {
		ws := listingtest.MakeWidgets(tc.items)
		l := newWidgets(b, listingtest.NewSchema(b), pagetokentest.Codec(b))

		first, err := l.List(b.Context(), listingtest.Subject, tc.req, ws)
		if err != nil {
			b.Fatal(err)
		}

		b.Run(tc.name+"/First", func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				if _, err := l.List(b.Context(), listingtest.Subject, tc.req, ws); err != nil {
					b.Fatal(err)
				}
			}
		})

		if first.NextPageToken == "" {
			continue
		}

		later := tc.req
		later.PageToken = first.NextPageToken

		b.Run(tc.name+"/Later", func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				if _, err := l.List(b.Context(), listingtest.Subject, later, ws); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
