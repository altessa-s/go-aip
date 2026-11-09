// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package parser

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	filter "github.com/altessa-s/go-aip/filter"
)

// maxParseErrorBytes bounds the text of any parse error, whatever the expression.
const maxParseErrorBytes = 1 << 10

// mismatched returns the NEW-2 input: a distinct expression of n bytes made of mismatched parentheses, on which cel-go
// reports 100 errors, each with the whole source line.
func mismatched(i, n int) string {
	var b strings.Builder

	fmt.Fprintf(&b, "a%d == ", i)

	for b.Len() < n {
		b.WriteString(")( ")
	}

	return b.String()[:n]
}

// TestParseError_Compact pins NEW-2: a parse error names the first syntax error with its position and a short snippet, and its
// text stays bounded however many errors the expression holds or however long its tokens are.
func TestParseError_Compact(t *testing.T) {
	t.Parallel()

	p, err := New()
	require.NoError(t, err)

	for _, tc := range []struct {
		name, expr string
		want       []string
	}{
		{"mismatched parentheses", mismatched(0, 4090), []string{`1:7: `, `near "a0 == )( )(`, " more)"}},
		{"unterminated string", `a == "` + strings.Repeat("x", 4000), []string{`1:6: `, `near "a == \"xxx`}},
		{"second line", "a == 1 &&\n  b == )", []string{`2:8: `, `near "  b == )"`}},
		{"multibyte snippet", strings.Repeat("é", 40) + " == )", []string{`near "ééé`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := p.Parse(t.Context(), tc.expr)
			require.ErrorIs(t, err, filter.ErrParseFailed)
			require.LessOrEqual(t, len(err.Error()), maxParseErrorBytes, err.Error())

			for _, want := range tc.want {
				require.Contains(t, err.Error(), want)
			}
		})
	}
}

// TestParseError_NegativeCacheBytes pins NEW-2: distinct invalid filters at the length limit keep the negative cache within
// its byte bound, where each used to pin about 900 KB.
func TestParseError_NegativeCacheBytes(t *testing.T) {
	t.Parallel()

	p, err := New()
	require.NoError(t, err)

	for i := range 1000 {
		_, err := p.Parse(t.Context(), mismatched(i, 4090))
		require.ErrorIs(t, err, filter.ErrParseFailed)
	}

	_, _, failures := p.cache.stats()
	require.Equal(t, failureCacheSize, failures)
	require.LessOrEqual(t, p.cache.failures.Cost(), failureCacheBytes)
}

// TestParseError_LargeErrorNotCached pins NEW-2: an error longer than maxFailureBytes, such as a custom function's that echoes
// its input, is returned but not kept by the negative cache.
func TestParseError_LargeErrorNotCached(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64

	p, err := New(WithCustomFunctions(map[string]filter.CustomFunction{
		"echo": func([]filter.Node) (filter.Node, error) {
			calls.Add(1)
			return nil, errors.New(strings.Repeat("x", 2*maxFailureBytes))
		},
	}))
	require.NoError(t, err)

	for range 2 {
		_, err := p.Parse(t.Context(), "echo()")
		require.ErrorIs(t, err, filter.ErrInvalidExpression)
	}

	require.EqualValues(t, 2, calls.Load())

	_, _, failures := p.cache.stats()
	require.Zero(t, failures)
}

func TestSnippet(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		text         string
		line, column int
		want         string
	}{
		{"abc", 1, 1, "abc"},
		{strings.Repeat("a", 30) + "X" + strings.Repeat("b", 30), 1, 30, strings.Repeat("a", 24) + "X" + strings.Repeat("b", 23)},
		{"ab\ncd", 2, 0, "cd"},
		{"ab", 3, 0, ""},
		{"ab", 1, 40, ""},
		{"日本語", 1, 1, "日本語"},
	} {
		require.Equal(t, tc.want, snippet(tc.text, tc.line, tc.column), "%q %d:%d", tc.text, tc.line, tc.column)
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()

	require.Equal(t, "abc", truncate("abc", 3))
	require.Equal(t, "ab...", truncate("abc", 2))
	require.Equal(t, "a...", truncate("aé", 2), "a cut inside a rune backs off to its start")
}
