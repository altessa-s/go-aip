// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
)

func bounds(values ...any) []filter.Node {
	nodes := make([]filter.Node, 0, len(values))
	for _, v := range values {
		nodes = append(nodes, &filter.LiteralNode{Value: v})
	}

	return nodes
}

// TestSubstringBounds pins the bound arithmetic: end - start is computed only for ordered, non-negative bounds, so no
// input overflows int64.
func TestSubstringBounds(t *testing.T) {
	t.Parallel()

	accepted := map[string]struct {
		args                  []filter.Node
		start, length, guards int64
	}{
		"from":             {bounds(int64(1)), 1, -1, 1},
		"range":            {bounds(int64(1), int64(3)), 1, 2, 3},
		"empty range":      {bounds(int64(2), int64(2)), 2, 0, 2},
		"from zero":        {bounds(int64(0)), 0, -1, 0},
		"largest start":    {bounds(int64(math.MaxInt64)), math.MaxInt64, -1, math.MaxInt64},
		"widest range":     {bounds(int64(0), int64(math.MaxInt64)), 0, math.MaxInt64, math.MaxInt64},
		"largest bounds":   {bounds(int64(math.MaxInt64), int64(math.MaxInt64)), math.MaxInt64, 0, math.MaxInt64},
		"beyond int4":      {bounds(int64(1), int64(3_000_000_000)), 1, 2_999_999_999, 3_000_000_000},
		"one past int4":    {bounds(int64(math.MaxInt32) + 1), math.MaxInt32 + 1, -1, math.MaxInt32 + 1},
		"int4 start range": {bounds(int64(math.MaxInt32), int64(math.MaxInt32)+5), math.MaxInt32, 5, math.MaxInt32 + 5},
	}

	for name, tt := range accepted {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			start, length, guard, err := sqlbase.SubstringBounds(tt.args)
			require.NoError(t, err)
			require.Equal(t, tt.start, start)
			require.Equal(t, tt.length, length)
			require.Equal(t, tt.guards, guard)
		})
	}

	// substring(1, MinInt64) used to compute MinInt64 - 1, which wraps to MaxInt64 and passed the order check.
	rejected := map[string]struct {
		args []filter.Node
		want error
	}{
		"end wraps on subtraction": {bounds(int64(1), int64(math.MinInt64)), filter.ErrInvalidExpression},
		"end before start":         {bounds(int64(3), int64(1)), filter.ErrInvalidExpression},
		"negative start":           {bounds(int64(-1)), filter.ErrInvalidExpression},
		"most negative start":      {bounds(int64(math.MinInt64), int64(0)), filter.ErrInvalidExpression},
		"negative end":             {bounds(int64(0), int64(-1)), filter.ErrInvalidExpression},
		"largest start, end zero":  {bounds(int64(math.MaxInt64), int64(0)), filter.ErrInvalidExpression},
		"no bound":                 {bounds(), filter.ErrInvalidExpression},
		"three bounds":             {bounds(int64(0), int64(1), int64(2)), filter.ErrInvalidExpression},
		"not an int":               {bounds("1"), filter.ErrInvalidExpression},
		"not a literal":            {[]filter.Node{&filter.IdentNode{Name: "n"}}, filter.ErrUnsupportedOperation},
	}

	for name, tt := range rejected {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, _, _, err := sqlbase.SubstringBounds(tt.args)
			require.ErrorIs(t, err, tt.want)
		})
	}
}

func TestSubstringStart(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ start, limit, want int64 }{
		{0, math.MaxInt64, 1},
		{41, math.MaxInt64, 42},
		{math.MaxInt64 - 1, math.MaxInt64, math.MaxInt64},
		{math.MaxInt64, math.MaxInt64, math.MaxInt64},
		{math.MaxInt32 - 1, math.MaxInt32, math.MaxInt32},
		{math.MaxInt32, math.MaxInt32, math.MaxInt32},
		{math.MaxInt64, math.MaxInt32, math.MaxInt32},
	} {
		require.Equal(t, tt.want, sqlbase.SubstringStart(tt.start, tt.limit), "start %d, limit %d", tt.start, tt.limit)
	}
}
