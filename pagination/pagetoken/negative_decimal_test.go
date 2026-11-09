// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

func TestNegativeDecimalComparisonImmutable(t *testing.T) {
	t.Parallel()

	a, err := pagetoken.NewSortValue(decimal.RequireFromString("-1.5"))
	require.NoError(t, err)
	b, err := pagetoken.NewSortValue(decimal.RequireFromString("-2.5"))
	require.NoError(t, err)

	for range 8 {
		t.Run("concurrent", func(t *testing.T) {
			t.Parallel()

			for range 100 {
				require.Positive(t, a.Compare(b))
				value, ok := a.Value()
				require.True(t, ok)
				require.Equal(t, "-1.5", value.(decimal.Decimal).String())
			}
		})
	}
}
