// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
)

func TestDecimalFloatExactOrdering(t *testing.T) {
	t.Parallel()
	require.True(t, scalarCompare(filter.OpLT, decimal.RequireFromString("0.1"), float64(0.1)))
}

func TestExtremeDecimalComparisons(t *testing.T) {
	t.Parallel()

	huge := decimal.New(1, 2147483647)
	tiny := decimal.New(1, -2147483647)

	require.True(t, equal(huge, huge))
	require.True(t, scalarCompare(filter.OpGT, huge, decimal.NewFromInt(1)))
	require.True(t, scalarCompare(filter.OpLT, tiny, float64(1)))
	require.True(t, scalarCompare(filter.OpGT, huge, uint64(^uint64(0))))
}
