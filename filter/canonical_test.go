// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

// canonicalOf parses expr and returns its canonical encoding.
func canonicalOf(tb testing.TB, p *filterparser.Parser, expr string) string {
	tb.Helper()
	node, err := p.Parse(tb.Context(), expr)
	require.NoError(tb, err)

	return mustCanonical(tb, node)
}

func TestCanonicalIgnoresSpelling(t *testing.T) {
	t.Parallel()

	p, err := filterparser.New()
	require.NoError(t, err)

	tests := []struct {
		name string
		a, b string
	}{
		{"whitespace", `a > 0 && b == "x"`, `a>0&&b=="x"`},
		{"global call form", `name.startsWith("n")`, `startsWith(name, "n")`},
		{"parentheses", `(a == 1)`, `a == 1`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, canonicalOf(t, p, tc.a), canonicalOf(t, p, tc.b))
		})
	}
}

func TestCanonicalDistinguishesMeaning(t *testing.T) {
	t.Parallel()

	p, err := filterparser.New()
	require.NoError(t, err)

	pairs := [][2]string{
		{`a > 0`, `a > 1`},
		{`a > 0`, `a >= 0`},
		{`a == 1`, `a == "1"`},
		{`a == 1`, `a == 1.0`},
		{`a == 1 && b == 2`, `b == 2 && a == 1`},
		{`a == 1 && b == 2`, `a == 1 || b == 2`},
		{`!(a == 1)`, `a == 1`},
		{`a in [1, 2]`, `a in [2, 1]`},
		{`name.startsWith("n")`, `name.endsWith("n")`},
		{`a == "x:y"`, `a == "x" && y == ""`},
	}
	for _, pair := range pairs {
		require.NotEqual(t, canonicalOf(t, p, pair[0]), canonicalOf(t, p, pair[1]), "%s vs %s", pair[0], pair[1])
	}
}

func TestCanonicalLiterals(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 3, 1, 12, 0, 0, 123_000_000, time.UTC)
	elsewhere := at.In(time.FixedZone("UTC+3", 3*3600))
	lit := func(v any) string { return mustCanonical(t, &filter.LiteralNode{Value: v}) }

	require.Empty(t, mustCanonical(t, nil))
	require.Equal(t, lit(at), lit(elsewhere), "the same instant in another zone")
	require.NotEqual(t, lit(at), lit(at.Add(time.Millisecond)))
	require.NotEqual(t, lit(nil), lit(""))
	require.NotEqual(t, lit([]byte("x")), lit("x"))
	require.NotEqual(t, lit(int64(1)), lit(uint64(1)))
	require.NotEqual(t,
		mustCanonical(t, &filter.LiteralNode{Value: 0.1, Text: "0.10000000000000000001"}),
		mustCanonical(t, &filter.LiteralNode{Value: 0.1}), "the decimal spelling counts")
}

func mustCanonical(t testing.TB, n filter.Node) string {
	t.Helper()

	value, err := filter.Canonical(n)
	require.NoError(t, err)

	return value
}
