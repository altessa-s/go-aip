// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//nolint:mnd // Fixed durations and counts describe the conformance scenarios.
package requestidtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/requestid"
)

// Factory returns an empty store for one scenario. It is called once per
// scenario; scenarios run in parallel, so a shared backend must isolate
// them (a fresh collection or key prefix, say).
type Factory func(t *testing.T) requestid.Store

// Base is the time the scenarios start at. It lies on a millisecond, as
// every time the suite passes, so a store may truncate times to
// milliseconds.
var Base = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// Run runs the conformance suite of the [requestid.Store] contract against
// the stores newStore returns.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	scenarios := []struct {
		name string
		run  func(*testing.T, requestid.Store)
	}{
		{"ReserveAbsent", testReserveAbsent},
		{"ReserveLive", testReserveLive},
		{"ReserveExpired", testReserveExpired},
		{"CompleteOwned", testCompleteOwned},
		{"CompleteFailure", testCompleteFailure},
		{"CompleteOmitted", testCompleteOmitted},
		{"CompleteNotOwned", testCompleteNotOwned},
		{"CompletedExpires", testCompletedExpires},
		{"ReleaseOwned", testReleaseOwned},
		{"ReleaseNotOwned", testReleaseNotOwned},
		{"KeyIsolation", testKeyIsolation},
		{"BinaryValues", testBinaryValues},
		{"NoAliasing", testNoAliasing},
		{"ConcurrentReserve", testConcurrentReserve},
		{"CanceledContext", testCanceledContext},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			sc.run(t, newStore(t))
		})
	}
}

// Key returns a store key derived from name, shaped as the keys of a
// [requestid.Guard]: 64 hexadecimal characters.
func Key(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

func pending(token string, expiresAt time.Time) requestid.Entry {
	return requestid.Entry{State: requestid.StatePending, Token: token, Fingerprint: []byte("fp-" + token), ExpiresAt: expiresAt}
}

func completed(from requestid.Entry, expiresAt time.Time) requestid.Entry {
	return requestid.Entry{
		State: requestid.StateCompleted, Token: from.Token, Fingerprint: from.Fingerprint,
		Response: []byte("response"), ExpiresAt: expiresAt,
	}
}

func reserve(t *testing.T, s requestid.Store, key string, e requestid.Entry, now time.Time) (requestid.Entry, bool) {
	t.Helper()

	got, ok, err := s.Reserve(t.Context(), key, e, now)
	require.NoError(t, err)

	return got, ok
}

// mustReserve reserves key and fails unless the reservation succeeds.
func mustReserve(t *testing.T, s requestid.Store, key string, e requestid.Entry, now time.Time) {
	t.Helper()

	got, ok := reserve(t, s, key, e, now)
	require.True(t, ok, "Reserve must reserve an absent key")
	assertEntry(t, e, got)
}

// assertLive checks that key holds want at now, by a Reserve that must
// not reserve.
func assertLive(t *testing.T, s requestid.Store, key string, want requestid.Entry, now time.Time) {
	t.Helper()

	got, ok := reserve(t, s, key, pending("probe", now.Add(time.Hour)), now)
	require.False(t, ok, "Reserve must not reserve a key with a live entry")
	assertEntry(t, want, got)
}

func assertEntry(t *testing.T, want, got requestid.Entry) {
	t.Helper()

	assert.Equal(t, want.State, got.State, "State")
	assert.Equal(t, want.Token, got.Token, "Token")
	assert.Equal(t, string(want.Fingerprint), string(got.Fingerprint), "Fingerprint")
	assert.Equal(t, string(want.Response), string(got.Response), "Response")
	assert.Equal(t, string(want.Failure), string(got.Failure), "Failure")
	assert.Equal(t, want.ResponseOmitted, got.ResponseOmitted, "ResponseOmitted")
	assert.True(t, want.ExpiresAt.Truncate(time.Millisecond).Equal(got.ExpiresAt.Truncate(time.Millisecond)),
		"ExpiresAt: want %v, got %v", want.ExpiresAt, got.ExpiresAt)
}

func testReserveAbsent(t *testing.T, s requestid.Store) {
	mustReserve(t, s, Key(t.Name()), pending("a", Base.Add(time.Minute)), Base)
}

func testReserveLive(t *testing.T, s requestid.Store) {
	key := Key(t.Name())
	first := pending("a", Base.Add(time.Minute))
	mustReserve(t, s, key, first, Base)

	// Still live one millisecond before it expires.
	assertLive(t, s, key, first, Base.Add(time.Minute-time.Millisecond))
}

func testReserveExpired(t *testing.T, s requestid.Store) {
	key := Key(t.Name())
	mustReserve(t, s, key, pending("a", Base.Add(time.Minute)), Base)

	// Expired exactly at ExpiresAt: a new reservation takes the key.
	at := Base.Add(time.Minute)
	second := pending("b", at.Add(time.Minute))
	mustReserve(t, s, key, second, at)
	assertLive(t, s, key, second, at)
}

func testCompleteOwned(t *testing.T, s requestid.Store) {
	key := Key(t.Name())
	p := pending("a", Base.Add(time.Minute))
	mustReserve(t, s, key, p, Base)

	done := completed(p, Base.Add(24*time.Hour))
	require.NoError(t, s.Complete(t.Context(), key, "a", done, Base.Add(time.Second)))

	// The retention, not the lease, now bounds the entry.
	assertLive(t, s, key, done, Base.Add(time.Hour))
}

func testCompleteFailure(t *testing.T, s requestid.Store) {
	key := Key(t.Name())
	p := pending("a", Base.Add(time.Minute))
	mustReserve(t, s, key, p, Base)

	done := completed(p, Base.Add(time.Hour))
	done.Response, done.Failure = nil, []byte("status")
	require.NoError(t, s.Complete(t.Context(), key, "a", done, Base))
	assertLive(t, s, key, done, Base)
}

func testCompleteOmitted(t *testing.T, s requestid.Store) {
	key := Key(t.Name())
	p := pending("a", Base.Add(time.Minute))
	mustReserve(t, s, key, p, Base)

	done := completed(p, Base.Add(time.Hour))
	done.Response, done.ResponseOmitted = nil, true
	require.NoError(t, s.Complete(t.Context(), key, "a", done, Base))
	assertLive(t, s, key, done, Base)
}

func testCompleteNotOwned(t *testing.T, s requestid.Store) {
	t.Run("Absent", func(t *testing.T) {
		key := Key(t.Name())
		err := s.Complete(t.Context(), key, "a", completed(pending("a", Base), Base.Add(time.Hour)), Base)
		require.ErrorIs(t, err, requestid.ErrReservationLost)
		mustReserve(t, s, key, pending("b", Base.Add(time.Minute)), Base)
	})

	t.Run("OtherToken", func(t *testing.T) {
		key := Key(t.Name())
		p := pending("a", Base.Add(time.Minute))
		mustReserve(t, s, key, p, Base)

		err := s.Complete(t.Context(), key, "b", completed(p, Base.Add(time.Hour)), Base)
		require.ErrorIs(t, err, requestid.ErrReservationLost)
		assertLive(t, s, key, p, Base)
	})

	t.Run("LeaseExpired", func(t *testing.T) {
		key := Key(t.Name())
		p := pending("a", Base.Add(time.Minute))
		mustReserve(t, s, key, p, Base)

		err := s.Complete(t.Context(), key, "a", completed(p, Base.Add(time.Hour)), Base.Add(time.Minute))
		require.ErrorIs(t, err, requestid.ErrReservationLost)
	})

	t.Run("TakenOver", func(t *testing.T) {
		key := Key(t.Name())
		p := pending("a", Base.Add(time.Minute))
		mustReserve(t, s, key, p, Base)

		at := Base.Add(2 * time.Minute)
		second := pending("b", at.Add(time.Minute))
		mustReserve(t, s, key, second, at)

		err := s.Complete(t.Context(), key, "a", completed(p, at.Add(time.Hour)), at)
		require.ErrorIs(t, err, requestid.ErrReservationLost)
		assertLive(t, s, key, second, at)
	})

	t.Run("Completed", func(t *testing.T) {
		key := Key(t.Name())
		p := pending("a", Base.Add(time.Minute))
		mustReserve(t, s, key, p, Base)

		done := completed(p, Base.Add(time.Hour))
		require.NoError(t, s.Complete(t.Context(), key, "a", done, Base))

		again := done
		again.Response = []byte("other")
		require.ErrorIs(t, s.Complete(t.Context(), key, "a", again, Base), requestid.ErrReservationLost)
		assertLive(t, s, key, done, Base)
	})
}

func testCompletedExpires(t *testing.T, s requestid.Store) {
	key := Key(t.Name())
	p := pending("a", Base.Add(time.Minute))
	mustReserve(t, s, key, p, Base)
	require.NoError(t, s.Complete(t.Context(), key, "a", completed(p, Base.Add(time.Hour)), Base))

	at := Base.Add(time.Hour)
	mustReserve(t, s, key, pending("b", at.Add(time.Minute)), at)
}

func testReleaseOwned(t *testing.T, s requestid.Store) {
	key := Key(t.Name())
	mustReserve(t, s, key, pending("a", Base.Add(time.Minute)), Base)
	require.NoError(t, s.Release(t.Context(), key, "a", Base))

	mustReserve(t, s, key, pending("b", Base.Add(time.Minute)), Base)
}

func testReleaseNotOwned(t *testing.T, s requestid.Store) {
	t.Run("Absent", func(t *testing.T) {
		require.ErrorIs(t, s.Release(t.Context(), Key(t.Name()), "a", Base), requestid.ErrReservationLost)
	})

	t.Run("OtherToken", func(t *testing.T) {
		key := Key(t.Name())
		p := pending("a", Base.Add(time.Minute))
		mustReserve(t, s, key, p, Base)

		require.ErrorIs(t, s.Release(t.Context(), key, "b", Base), requestid.ErrReservationLost)
		assertLive(t, s, key, p, Base)
	})

	t.Run("LeaseExpired", func(t *testing.T) {
		key := Key(t.Name())
		mustReserve(t, s, key, pending("a", Base.Add(time.Minute)), Base)

		require.ErrorIs(t, s.Release(t.Context(), key, "a", Base.Add(time.Minute)), requestid.ErrReservationLost)
	})

	t.Run("Completed", func(t *testing.T) {
		key := Key(t.Name())
		p := pending("a", Base.Add(time.Minute))
		mustReserve(t, s, key, p, Base)

		done := completed(p, Base.Add(time.Hour))
		require.NoError(t, s.Complete(t.Context(), key, "a", done, Base))

		require.ErrorIs(t, s.Release(t.Context(), key, "a", Base), requestid.ErrReservationLost)
		assertLive(t, s, key, done, Base)
	})
}

func testKeyIsolation(t *testing.T, s requestid.Store) {
	k1, k2 := Key(t.Name()+"/1"), Key(t.Name()+"/2")
	p1, p2 := pending("a", Base.Add(time.Minute)), pending("b", Base.Add(time.Minute))
	mustReserve(t, s, k1, p1, Base)
	mustReserve(t, s, k2, p2, Base)

	require.NoError(t, s.Complete(t.Context(), k1, "a", completed(p1, Base.Add(time.Hour)), Base))
	require.NoError(t, s.Release(t.Context(), k2, "b", Base))

	assertLive(t, s, k1, completed(p1, Base.Add(time.Hour)), Base)
	mustReserve(t, s, k2, pending("c", Base.Add(time.Minute)), Base)
}

func testBinaryValues(t *testing.T, s requestid.Store) {
	key := Key(t.Name())
	binary := make([]byte, 64<<10)

	for i := range binary {
		binary[i] = byte(i * 7)
	}

	p := pending("a", Base.Add(time.Minute))
	p.Fingerprint = []byte{0, 0xff, 0, 0x80}
	mustReserve(t, s, key, p, Base)

	done := completed(p, Base.Add(time.Hour))
	done.Response = binary
	require.NoError(t, s.Complete(t.Context(), key, "a", done, Base))
	assertLive(t, s, key, done, Base)
}

func testNoAliasing(t *testing.T, s requestid.Store) {
	key := Key(t.Name())
	p := pending("a", Base.Add(time.Minute))
	fp := []byte("fingerprint")
	p.Fingerprint = fp
	mustReserve(t, s, key, p, Base)

	fp[0] = 'X' // the caller's slice must not be the stored one

	got, ok := reserve(t, s, key, pending("b", Base.Add(time.Minute)), Base)
	require.False(t, ok)
	require.Equal(t, "fingerprint", string(got.Fingerprint))

	got.Fingerprint[0] = 'Y' // nor the returned one
	assertLive(t, s, key, requestid.Entry{
		State: requestid.StatePending, Token: "a", Fingerprint: []byte("fingerprint"), ExpiresAt: p.ExpiresAt,
	}, Base)
}

func testConcurrentReserve(t *testing.T, s requestid.Store) {
	const n = 32

	key := Key(t.Name())
	results := make([]requestid.Entry, n)
	won := make([]bool, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			results[i], won[i], errs[i] = s.Reserve(t.Context(), key, pending(string(rune('a'+i)), Base.Add(time.Minute)), Base)
		})
	}

	wg.Wait()

	winner := ""

	for i := range n {
		require.NoError(t, errs[i])

		if won[i] {
			require.Empty(t, winner, "two callers reserved the same key")

			winner = results[i].Token
		}
	}

	require.NotEmpty(t, winner, "no caller reserved the key")

	for i := range n {
		assert.Equal(t, winner, results[i].Token, "every caller must see the winning reservation")
	}
}

func testCanceledContext(t *testing.T, s requestid.Store) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	key := Key(t.Name())
	_, ok, err := s.Reserve(ctx, key, pending("a", Base.Add(time.Minute)), Base)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, ok)

	require.ErrorIs(t, s.Complete(ctx, key, "a", completed(pending("a", Base), Base.Add(time.Hour)), Base), context.Canceled)
	require.ErrorIs(t, s.Release(ctx, key, "a", Base), context.Canceled)

	// Nothing was reserved.
	mustReserve(t, s, key, pending("b", Base.Add(time.Minute)), Base)
}
