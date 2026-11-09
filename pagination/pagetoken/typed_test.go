// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

type position struct {
	Time int64  `json:"t"`
	ID   string `json:"i"`
}

func TestNewBindings_LengthPrefixesFilterParts(t *testing.T) {
	t.Parallel()

	b := pagetoken.NewBindings("list", "name:1", "ab", "c")
	assert.Equal(t, pagetoken.Bindings{Subject: "list", Sort: "name:1", Filter: "2:ab1:c"}, b)
	assert.NotEqual(t, b, pagetoken.NewBindings("list", "name:1", "a", "bc"))
	assert.Equal(t, pagetoken.Bindings{Subject: "list"}, pagetoken.NewBindings("list", ""))
	// The layout is persisted in issued tokens: an empty part and a multi-digit length stay framed as before.
	assert.Equal(t, "0:10:0123456789", pagetoken.NewBindings("list", "", "", "0123456789").Filter)
}

func TestTyped_RoundTrip(t *testing.T) {
	t.Parallel()

	typed := pagetoken.NewTyped[position](newCodec(t))
	token, err := typed.Issue(position{Time: 42, ID: "x"}, bindings)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	got, first, err := typed.Resolve(token, bindings)
	require.NoError(t, err)
	assert.False(t, first)
	assert.Equal(t, position{Time: 42, ID: "x"}, got)
}

func TestTyped_EmptyTokenIsFirstPage(t *testing.T) {
	t.Parallel()

	for name, typed := range map[string]pagetoken.Typed[position]{
		"codec":    pagetoken.NewTyped[position](newCodec(t)),
		"no codec": pagetoken.NewTyped[position](nil),
		"zero":     {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, first, err := typed.Resolve("", bindings)
			require.NoError(t, err)
			assert.True(t, first)
			assert.Zero(t, got)
		})
	}
}

func TestTyped_WithoutCodec(t *testing.T) {
	t.Parallel()

	typed := pagetoken.NewTyped[int](nil)
	token, err := typed.Issue(1, bindings)
	require.NoError(t, err)
	assert.Empty(t, token, "a list without a codec serves a single page")

	signed, err := pagetoken.NewTyped[int](newCodec(t)).Issue(1, bindings)
	require.NoError(t, err)
	_, first, err := typed.Resolve(signed, bindings)
	require.ErrorIs(t, err, pagetoken.ErrCodecRequired)
	assert.False(t, first)
	assert.Equal(t, pagetoken.FailureMalformed, pagetoken.Classify(err))
}

func TestTyped_Rejected(t *testing.T) {
	t.Parallel()

	c := newCodec(t)
	typed := pagetoken.NewTyped[int](c)
	token, err := typed.Issue(7, bindings)
	require.NoError(t, err)
	notJSON, err := c.Issue([]byte("{"), bindings)
	require.NoError(t, err)
	otherType, err := pagetoken.NewTyped[string](c).Issue("seven", bindings)
	require.NoError(t, err)

	tests := map[string]struct {
		token string
		b     pagetoken.Bindings
		want  error
	}{
		"garbage":        {token: "!!!", b: bindings, want: pagetoken.ErrInvalidToken},
		"not json":       {token: notJSON, b: bindings, want: pagetoken.ErrInvalidToken},
		"other type":     {token: otherType, b: bindings, want: pagetoken.ErrInvalidToken},
		"sort changed":   {token: token, b: pagetoken.Bindings{Sort: "x", Filter: bindings.Filter, Subject: bindings.Subject}, want: pagetoken.ErrSortChanged},
		"filter changed": {token: token, b: pagetoken.Bindings{Sort: bindings.Sort, Filter: "x", Subject: bindings.Subject}, want: pagetoken.ErrFilterChanged},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, first, err := typed.Resolve(tc.token, tc.b)
			require.ErrorIs(t, err, tc.want)
			assert.Zero(t, got)
			assert.False(t, first)
			assert.Equal(t, pagetoken.FailureMalformed, pagetoken.Classify(err))
		})
	}
}

func TestTyped_IssueErrors(t *testing.T) {
	t.Parallel()

	_, err := pagetoken.NewTyped[func()](newCodec(t)).Issue(func() {}, bindings)
	require.Error(t, err, "an unmarshalable cursor fails")

	_, err = pagetoken.NewTyped[string](newCodec(t)).Issue(string(make([]byte, pagetoken.MaxPayloadLength)), bindings)
	require.ErrorIs(t, err, pagetoken.ErrPayloadTooLarge)
}

func TestOffset(t *testing.T) {
	t.Parallel()

	c := newCodec(t)
	off := pagetoken.NewOffset(c)
	token, err := off.Issue(25, bindings)
	require.NoError(t, err)
	got, err := off.Resolve(token, bindings)
	require.NoError(t, err)
	assert.Equal(t, 25, got)

	got, err = off.Resolve("", bindings)
	require.NoError(t, err)
	assert.Zero(t, got, "an empty token starts at offset 0")

	_, err = off.Issue(-1, bindings)
	require.ErrorIs(t, err, pagetoken.ErrNegativeOffset)
	assert.Equal(t, pagetoken.FailureNone, pagetoken.Classify(err), "a negative offset is a caller bug, not a token failure")

	negative, err := pagetoken.NewTyped[int](c).Issue(-3, bindings)
	require.NoError(t, err)
	_, err = off.Resolve(negative, bindings)
	require.ErrorIs(t, err, pagetoken.ErrInvalidToken)

	empty, err := pagetoken.NewOffset(nil).Issue(5, bindings)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestClassify(t *testing.T) {
	t.Parallel()

	wrap := func(err error) error { return fmt.Errorf("list: %w", err) }

	tests := map[string]struct {
		err  error
		want pagetoken.Failure
	}{
		"nil":              {err: nil, want: pagetoken.FailureNone},
		"other":            {err: errors.New("boom"), want: pagetoken.FailureNone},
		"invalid":          {err: wrap(pagetoken.ErrInvalidToken), want: pagetoken.FailureMalformed},
		"sort changed":     {err: wrap(pagetoken.ErrSortChanged), want: pagetoken.FailureMalformed},
		"filter changed":   {err: wrap(pagetoken.ErrFilterChanged), want: pagetoken.FailureMalformed},
		"subject mismatch": {err: wrap(pagetoken.ErrSubjectMismatch), want: pagetoken.FailureMalformed},
		"codec required":   {err: wrap(pagetoken.ErrCodecRequired), want: pagetoken.FailureMalformed},
		"expired":          {err: wrap(pagetoken.ErrExpiredToken), want: pagetoken.FailureExpired},
		"payload too big":  {err: wrap(pagetoken.ErrPayloadTooLarge), want: pagetoken.FailureNone},
		"weak key":         {err: wrap(pagetoken.ErrWeakKey), want: pagetoken.FailureNone},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, pagetoken.Classify(tc.err))
		})
	}

	assert.Equal(t, "malformed", pagetoken.FailureMalformed.String())
	assert.Equal(t, "expired", pagetoken.FailureExpired.String())
	assert.Empty(t, pagetoken.FailureNone.String())
}

func TestTyped_Expired(t *testing.T) {
	t.Parallel()

	issued := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	token, err := pagetoken.NewTyped[int](newCodec(t, pagetoken.WithClock(fixedClock(issued)))).Issue(1, bindings)
	require.NoError(t, err)

	later := pagetoken.NewTyped[int](newCodec(t, pagetoken.WithClock(fixedClock(issued.Add(pagetoken.DefaultTTL+time.Second)))))
	_, _, err = later.Resolve(token, bindings)
	require.ErrorIs(t, err, pagetoken.ErrExpiredToken)
	assert.Equal(t, pagetoken.FailureExpired, pagetoken.Classify(err))
}
