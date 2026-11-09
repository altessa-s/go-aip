// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

// FuzzCodec_Resolve feeds arbitrary tokens to Resolve: it must never panic
// and must reject everything it did not issue.
func FuzzCodec_Resolve(f *testing.F) {
	// A fixed clock makes the issued token identical in every fuzz worker.
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c, err := pagetoken.New(key, pagetoken.WithClock(func() time.Time { return at }))
	require.NoError(f, err)

	valid, _ := c.Issue([]byte("p"), bindings)
	f.Add(valid)
	f.Add("")
	f.Add("AAAA")

	f.Fuzz(func(t *testing.T, token string) {
		payload, err := c.Resolve(token, bindings)
		if err == nil {
			require.Equal(t, valid, token, "accepted a token it did not issue")
			require.Equal(t, "p", string(payload))

			return
		}

		require.True(t, errors.Is(err, pagetoken.ErrInvalidToken) || errors.Is(err, pagetoken.ErrExpiredToken) ||
			errors.Is(err, pagetoken.ErrSortChanged) || errors.Is(err, pagetoken.ErrFilterChanged) ||
			errors.Is(err, pagetoken.ErrSubjectMismatch), "unexpected error: %v", err)
	})
}

// FuzzCodec_ResolveEncrypted feeds arbitrary tokens to a codec that accepts
// only encrypted tokens: it must never panic and must reject everything but
// the tokens it issued.
func FuzzCodec_ResolveEncrypted(f *testing.F) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c, err := pagetoken.New(key, pagetoken.WithStrictEncryption(true), pagetoken.WithClock(func() time.Time { return at }))
	require.NoError(f, err)

	valid, _ := c.Issue([]byte("p"), bindings)
	f.Add(valid)
	f.Add("")
	f.Add("AgAA")

	f.Fuzz(func(t *testing.T, token string) {
		payload, err := c.Resolve(token, bindings)
		if err == nil {
			require.Equal(t, "p", string(payload), "accepted a token it did not issue")
			return
		}

		require.True(t, errors.Is(err, pagetoken.ErrInvalidToken) || errors.Is(err, pagetoken.ErrExpiredToken) ||
			errors.Is(err, pagetoken.ErrSortChanged) || errors.Is(err, pagetoken.ErrFilterChanged) ||
			errors.Is(err, pagetoken.ErrSubjectMismatch), "unexpected error: %v", err)
	})
}
