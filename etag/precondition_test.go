// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package etag_test

import (
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/etag"

	coreetag "github.com/altessa-s/go-atlas-core/encoding/etag"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestPreconditionCheck(t *testing.T) {
	t.Parallel()

	cur := coreetag.Strong("abc")
	weakCur := coreetag.Weak("abc")

	tests := []struct {
		name    string
		opts    []etag.Option
		sent    string
		current coreetag.Tag
		err     error
		reason  string
	}{
		{name: "absent etag is no precondition", sent: "", current: cur},
		{name: "absent etag on a missing resource", sent: "", current: coreetag.Tag{}},
		{name: "matching strong", sent: `"abc"`, current: cur},
		{name: "matching bare", sent: "abc", current: cur},
		{name: "matching weak echoed", sent: `W/"abc"`, current: weakCur},
		{name: "stale", sent: `"old"`, current: cur, err: coreetag.ErrMismatch},
		{name: "etag for a missing resource", sent: `"abc"`, current: coreetag.Tag{}, err: coreetag.ErrMismatch},
		{name: "exact refuses a weak rewrite of a strong tag", sent: `W/"abc"`, current: cur, err: coreetag.ErrMismatch},
		{name: "exact refuses a strong rewrite of a weak tag", sent: `"abc"`, current: weakCur, err: coreetag.ErrMismatch},
		{name: "strong comparison refuses weak tags", opts: []etag.Option{etag.WithComparison(etag.CompareStrong)},
			sent: `W/"abc"`, current: weakCur, err: coreetag.ErrMismatch},
		{name: "strong comparison accepts strong tags", opts: []etag.Option{etag.WithComparison(etag.CompareStrong)},
			sent: `"abc"`, current: cur},
		{name: "weak comparison ignores weakness", opts: []etag.Option{etag.WithComparison(etag.CompareWeak)},
			sent: `"abc"`, current: weakCur},
		{name: "weak comparison refuses another value", opts: []etag.Option{etag.WithComparison(etag.CompareWeak)},
			sent: `W/"x"`, current: weakCur, err: coreetag.ErrMismatch},
		{name: "unknown comparison is exact", opts: []etag.Option{etag.WithComparison(etag.Comparison(99))},
			sent: `"abc"`, current: weakCur, err: coreetag.ErrMismatch},
		{name: "required etag missing", opts: []etag.Option{etag.WithRequired(true), etag.WithField("book.etag")},
			sent: "", current: cur, err: coreetag.ErrMissing, reason: etag.ReasonRequired},
		{name: "malformed", opts: []etag.Option{etag.WithField("book.etag")},
			sent: `"abc`, current: cur, err: coreetag.ErrInvalidTag, reason: etag.ReasonInvalid},
		{name: "bare refused by strict quotes", opts: []etag.Option{etag.WithStrictQuotes(true)},
			sent: "abc", current: cur, err: coreetag.ErrInvalidTag, reason: etag.ReasonInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := etag.NewPrecondition(tt.opts...).Check(tt.sent, tt.current)
			if tt.err == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tt.err)

			if tt.reason == "" {
				require.ErrorIs(t, err, coreerrs.ErrConflict, "a mismatch is ABORTED")
				return
			}

			fe, ok := errors.AsType[*coreerrs.FieldError](err)
			require.True(t, ok, "err = %v", err)
			require.Equal(t, tt.reason, fe.Reason)
			require.NotErrorIs(t, err, coreerrs.ErrConflict)
		})
	}
}

func TestPreconditionFieldName(t *testing.T) {
	t.Parallel()

	_, err := etag.Precondition{}.Parse("\x00")
	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok)
	require.Equal(t, etag.FieldName, fe.Field, "the zero Precondition uses the defaults")

	_, err = etag.NewPrecondition(etag.WithField("book.etag")).Parse("\x00")
	fe, ok = errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok)
	require.Equal(t, "book.etag", fe.Field)
}

func TestPreconditionMatch(t *testing.T) {
	t.Parallel()

	var p etag.Precondition

	require.NoError(t, p.Match(coreetag.Tag{}, coreetag.Tag{}))
	require.NoError(t, p.Match(coreetag.Tag{}, coreetag.Strong("x")))
	require.NoError(t, p.Match(coreetag.Strong("x"), coreetag.Strong("x")))
	require.ErrorIs(t, p.Match(coreetag.Strong("x"), coreetag.Tag{}), coreetag.ErrMismatch)
}

func TestPreconditionParseRevision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		opts   []etag.Option
		sent   string
		rev    int64
		ok     bool
		err    error
		reason string
	}{
		{name: "absent", sent: ""},
		{name: "revision", sent: `W/"7"`, rev: 7, ok: true},
		{name: "max usable revision", sent: etag.FromRevision[int64](math.MaxInt64 - 1).String(), rev: math.MaxInt64 - 1, ok: true},
		{name: "required", opts: []etag.Option{etag.WithRequired(true)}, err: coreetag.ErrMissing, reason: etag.ReasonRequired},
		{name: "zero needs singleton", sent: `W/"0"`, err: coreetag.ErrNotVersion, reason: etag.ReasonInvalid},
		{name: "zero for a singleton", opts: []etag.Option{etag.WithSingleton(true)}, sent: `W/"0"`, rev: 0, ok: true},
		{name: "max int64 would overflow", sent: etag.FromRevision[int64](math.MaxInt64).String(), err: coreetag.ErrNotVersion, reason: etag.ReasonInvalid},
		{name: "strong", sent: `"7"`, err: coreetag.ErrNotVersion, reason: etag.ReasonInvalid},
		{name: "bare number", sent: `7`, err: coreetag.ErrNotVersion, reason: etag.ReasonInvalid},
		{name: "not canonical", sent: `W/"07"`, err: coreetag.ErrNotVersion, reason: etag.ReasonInvalid},
		{name: "malformed", sent: `W/"7`, err: coreetag.ErrInvalidTag, reason: etag.ReasonInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rev, ok, err := etag.NewPrecondition(tt.opts...).ParseRevision(tt.sent)
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)

				fe, isField := errors.AsType[*coreerrs.FieldError](err)
				require.True(t, isField)
				require.Equal(t, tt.reason, fe.Reason)
				require.False(t, ok)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.rev, rev)
		})
	}
}
