// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package etag_test

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/etag"

	coreetag "github.com/altessa-s/go-atlas-core/encoding/etag"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		opts []etag.Option
		want coreetag.Tag
		err  error
	}{
		{name: "strong", in: `"abc"`, want: coreetag.Strong("abc")},
		{name: "weak", in: `W/"abc"`, want: coreetag.Weak("abc")},
		{name: "surrounding white space", in: " \t\"abc\"\n", want: coreetag.Strong("abc")},
		{name: "bare value is strong", in: "abc-123_~", want: coreetag.Strong("abc-123_~")},
		{name: "bare obs-text", in: "caf\xc3\xa9", want: coreetag.Strong("caf\xc3\xa9")},
		{name: "bare trimmed", in: " abc ", want: coreetag.Strong("abc")},
		{name: "empty", in: "", err: coreetag.ErrMissing},
		{name: "blank", in: "   ", err: coreetag.ErrInvalidTag},
		{name: "empty opaque tag", in: `""`, err: coreetag.ErrInvalidTag},
		{name: "unclosed", in: `"abc`, err: coreetag.ErrInvalidTag},
		{name: "trailing content", in: `"abc"x`, err: coreetag.ErrInvalidTag},
		{name: "lowercase weak prefix", in: `w/"abc"`, err: coreetag.ErrInvalidTag},
		{name: "weak prefix without quotes", in: `W/abc`, err: coreetag.ErrInvalidTag},
		{name: "bare with space", in: "a b", err: coreetag.ErrInvalidTag},
		{name: "bare with quote", in: `a"b`, err: coreetag.ErrInvalidTag},
		{name: "bare with control", in: "a\x01b", err: coreetag.ErrInvalidTag},
		{name: "quoted with control", in: "\"a\x7fb\"", err: coreetag.ErrInvalidTag},
		{name: "strict quotes refuse bare", in: "abc", opts: []etag.Option{etag.WithStrictQuotes(true)}, err: coreetag.ErrInvalidTag},
		{name: "strict quotes accept quoted", in: `"abc"`, opts: []etag.Option{etag.WithStrictQuotes(true)}, want: coreetag.Strong("abc")},
		{name: "too long", in: `"` + strings.Repeat("a", etag.DefaultMaxLength) + `"`, err: coreetag.ErrInvalidTag},
		{name: "max length unlimited", in: `"` + strings.Repeat("a", etag.DefaultMaxLength) + `"`,
			opts: []etag.Option{etag.WithMaxLength(0)}, want: coreetag.Strong(strings.Repeat("a", etag.DefaultMaxLength))},
		{name: "custom max length", in: `"abcd"`, opts: []etag.Option{etag.WithMaxLength(5)}, err: coreetag.ErrInvalidTag},
		{name: "negative max length keeps default", in: `"` + strings.Repeat("a", etag.DefaultMaxLength) + `"`,
			opts: []etag.Option{etag.WithMaxLength(-1)}, err: coreetag.ErrInvalidTag},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := etag.Parse(tt.in, tt.opts...)
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
				require.True(t, got.IsZero())

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseRoundTrip(t *testing.T) {
	t.Parallel()

	for _, tag := range []coreetag.Tag{coreetag.Strong("x"), coreetag.Weak("x"), etag.FromRevision[int64](42), etag.FromRevision[uint64](math.MaxUint64)} {
		got, err := etag.Parse(tag.String())
		require.NoError(t, err)
		require.Equal(t, tag, got)
	}
}

func TestSentinelsAreCoreSentinels(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, coreetag.ErrMissing, coreetag.ErrMissing)
	require.ErrorIs(t, coreetag.ErrInvalidTag, coreetag.ErrInvalidTag)
	require.ErrorIs(t, coreetag.ErrMismatch, coreetag.ErrMismatch)
	require.ErrorIs(t, coreetag.ErrNotVersion, coreetag.ErrNotVersion)
}

func TestFromRevision(t *testing.T) {
	t.Parallel()

	require.Equal(t, `W/"0"`, etag.FromRevision[int64](0).String())
	require.Equal(t, `W/"7"`, etag.FromRevision[int64](7).String())
	require.Equal(t, coreetag.FromVersion(7), etag.FromRevision[int64](7), "the go-atlas-core version tag")
	require.True(t, etag.FromRevision[int64](-1).IsZero())
	require.Equal(t, `W/"18446744073709551615"`, etag.FromRevision[uint64](math.MaxUint64).String())
}

func TestRevision(t *testing.T) {
	t.Parallel()

	v, err := etag.Revision[int64](etag.FromRevision[int64](math.MaxInt64))
	require.NoError(t, err)
	require.Equal(t, int64(math.MaxInt64), v)

	u, err := etag.Revision[uint64](etag.FromRevision[uint64](math.MaxUint64))
	require.NoError(t, err)
	require.Equal(t, uint64(math.MaxUint64), u)

	refused := []coreetag.Tag{
		{}, coreetag.Strong("7"), coreetag.Weak("07"), coreetag.Weak("-1"), coreetag.Weak("+1"), coreetag.Weak("1a"),
		coreetag.Weak("18446744073709551616"),
	}
	for _, tag := range refused {
		_, err = etag.Revision[uint64](tag)
		require.ErrorIs(t, err, coreetag.ErrNotVersion, "tag %q", tag)
	}

	_, err = etag.Revision[int64](etag.FromRevision[uint64](math.MaxInt64 + 1))
	require.ErrorIs(t, err, coreetag.ErrNotVersion, "beyond int64")
}

func TestComparison(t *testing.T) {
	t.Parallel()

	// RFC 9110 §8.8.3.2 comparison table.
	tests := []struct {
		a, b         coreetag.Tag
		strong, weak bool
	}{
		{a: coreetag.Weak("1"), b: coreetag.Weak("1"), strong: false, weak: true},
		{a: coreetag.Weak("1"), b: coreetag.Weak("2"), strong: false, weak: false},
		{a: coreetag.Weak("1"), b: coreetag.Strong("1"), strong: false, weak: true},
		{a: coreetag.Strong("1"), b: coreetag.Strong("1"), strong: true, weak: true},
	}
	for _, tt := range tests {
		require.Equal(t, tt.strong, tt.a.StrongMatch(tt.b), "%s strong %s", tt.a, tt.b)
		require.Equal(t, tt.weak, tt.a.WeakMatch(tt.b), "%s weak %s", tt.a, tt.b)
	}
}
