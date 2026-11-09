// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
)

// BenchmarkListTranslate measures the per-request work of a list before I/O: compiling the request and translating its filter.
func BenchmarkListTranslate(b *testing.B) {
	s := listingtest.NewSchema(b)
	l, err := NewListEngine[listingtest.Widget](nil, nil, s, WithListFieldPaths(listingtest.FieldPaths()))
	require.NoError(b, err)

	req := listing.Request{PageSize: 50, Filter: `kind in ["KIND_A", "KIND_B"] && priority > 1 && name.startsWith("n")`, OrderBy: "kind, priority desc"}

	for b.Loop() {
		q, err := s.Compile(b.Context(), req)
		if err != nil {
			b.Fatal(err)
		}

		if _, err = l.translate(q.Filter); err != nil {
			b.Fatal(err)
		}
	}
}
