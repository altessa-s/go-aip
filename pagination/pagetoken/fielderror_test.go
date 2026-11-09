// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/pagination/pagetoken"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestFieldError(t *testing.T) {
	t.Parallel()

	wrap := func(err error) error { return fmt.Errorf("list: %w", err) }
	errBoom := errors.New("boom")

	tests := []struct {
		name   string
		err    error
		reason string // empty: err is returned unchanged
	}{
		{name: "invalid", err: wrap(pagetoken.ErrInvalidToken), reason: "malformed"},
		{name: "sort changed", err: wrap(pagetoken.ErrSortChanged), reason: "malformed"},
		{name: "filter changed", err: wrap(pagetoken.ErrFilterChanged), reason: "malformed"},
		{name: "subject mismatch", err: wrap(pagetoken.ErrSubjectMismatch), reason: "malformed"},
		{name: "codec required", err: pagetoken.ErrCodecRequired, reason: "malformed"},
		{name: "expired", err: wrap(pagetoken.ErrExpiredToken), reason: "expired"},
		{name: "other", err: errBoom},
		{name: "weak key", err: wrap(pagetoken.ErrWeakKey)},
		{name: "payload too large", err: pagetoken.ErrPayloadTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := pagetoken.FieldError(tc.err, "pageToken")
			if tc.reason == "" {
				require.Equal(t, tc.err, got, "the error must be returned unchanged")
				require.NotErrorIs(t, got, coreerrs.ErrInvalidArgument)

				return
			}

			fe, ok := errors.AsType[*coreerrs.FieldError](got)
			require.True(t, ok)
			require.Equal(t, "pageToken", fe.Field)
			require.Equal(t, tc.reason, fe.Reason)
			require.ErrorIs(t, got, coreerrs.ErrInvalidArgument)
			require.ErrorIs(t, got, tc.err)
		})
	}

	require.NoError(t, pagetoken.FieldError(nil, "pageToken"))
}

func TestTokenFieldError(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("list: %w", pagetoken.ErrExpiredToken)
	fe, ok := errors.AsType[*coreerrs.FieldError](pagetoken.TokenFieldError(err))
	require.True(t, ok)
	require.Equal(t, pagetoken.TokenField, fe.Field)
	require.Equal(t, "pageToken", fe.Field)
	require.Equal(t, "expired", fe.Reason)
	require.ErrorIs(t, fe, pagetoken.ErrExpiredToken)

	errBoom := errors.New("boom")
	require.Equal(t, errBoom, pagetoken.TokenFieldError(errBoom))
	require.NoError(t, pagetoken.TokenFieldError(nil))
}

func TestResolvePageSize(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		requested int32
		want      int
	}{
		"zero is default": {requested: 0, want: 25},
		"within range":    {requested: 7, want: 7},
		"clamped":         {requested: 1000, want: 100},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := pagetoken.ResolvePageSize(tc.requested, 25, 100)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	_, err := pagetoken.ResolvePageSize(int64(-1), 25, 100)
	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok)
	require.Equal(t, pagetoken.SizeField, fe.Field)
	require.Equal(t, "pageSize", fe.Field)
	require.Equal(t, "must not be negative", fe.Reason)
	require.ErrorIs(t, err, coreerrs.ErrInvalidArgument)
}
