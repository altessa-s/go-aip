// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"math"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

func TestDecodeSortValueRoundTripsSpecialValues(t *testing.T) {
	t.Parallel()

	for _, value := range []struct {
		name string
		in   any
		kind filter.FieldKind
		want any
	}{
		{name: "positive infinity", in: math.Inf(1), kind: filter.FieldKindFloat, want: math.Inf(1)},
		{name: "negative infinity", in: math.Inf(-1), kind: filter.FieldKindFloat, want: math.Inf(-1)},
		{name: "non UTF-8 string", in: string([]byte{0xff, 0xfe}), kind: filter.FieldKindString, want: string([]byte{0xff, 0xfe})},
	} {
		t.Run(value.name, func(t *testing.T) {
			t.Parallel()

			sortValue, err := pagetoken.NewSortValue(value.in)
			require.NoError(t, err)
			got, err := decodeSortValue(sortValue, DefaultSQLKind(value.kind))
			require.NoError(t, err)
			require.Equal(t, value.want, got)
		})
	}
}

func TestSortValueExposesDecimalWithoutFormatting(t *testing.T) {
	t.Parallel()

	want := decimal.RequireFromString("1e6111")
	sortValue, err := pagetoken.NewSortValue(want)
	require.NoError(t, err)
	got, err := decodeSortValue(sortValue, DefaultSQLKind(filter.FieldKindDecimal))
	require.NoError(t, err)
	require.Equal(t, want, got)
}
