// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package requestid

import (
	"context"
	"time"
)

// State is the state of a stored [Entry].
type State int

const (
	// StatePending is a reservation: the first request with the ID is
	// running. It expires at the end of its lease, so a crashed handler
	// does not block the ID forever.
	StatePending State = iota + 1
	// StateCompleted is a finished request whose outcome is replayed to
	// every duplicate until the entry expires.
	StateCompleted
)

// Entry is the record a [Store] keeps under a key. The store treats every
// field but State, Token and ExpiresAt as opaque.
type Entry struct {
	// State is pending or completed.
	State State
	// Token identifies the owner of a pending reservation; only its owner
	// completes or releases it.
	Token string
	// Fingerprint is the hash of the request parameters.
	Fingerprint []byte
	// Response is the serialized response of a completed request that
	// succeeded.
	Response []byte
	// Failure is the serialized google.rpc.Status of a completed request
	// whose failure is replayed (see [WithCacheError]).
	Failure []byte
	// ResponseOmitted marks a completed request that succeeded with a
	// response too large to store (see [WithMaxResponseSize]).
	ResponseOmitted bool
	// ExpiresAt is when the entry stops existing: the end of the lease of
	// a reservation, the end of the retention of a completed request. An
	// entry is live while now is before ExpiresAt.
	ExpiresAt time.Time
}

// Store keeps the deduplication entries. Implementations must be safe for
// concurrent use and every method must be atomic with respect to the others
// on the same key, across every process sharing the store. Keys are opaque
// hexadecimal strings of 64 characters.
//
// now is the current time of the caller, the only clock of the contract: an
// entry whose ExpiresAt is not after now is treated as absent. A store may
// also drop it physically at any later time (a TTL index, say), but never
// while it is live, except under documented capacity limits where it must
// fail [Store.Reserve] rather than drop a live entry.
//
// Byte slices passed in or returned must not be retained or modified by the
// other side; a nil and an empty slice are equivalent. ExpiresAt may be
// truncated to millisecond precision.
//
// The requestidtest package holds the conformance suite every
// implementation must pass.
type Store interface {
	// Reserve stores pending under key unless a live entry exists. It
	// returns the stored entry and true when it reserved, or the live entry
	// and false otherwise.
	Reserve(ctx context.Context, key string, pending Entry, now time.Time) (Entry, bool, error)
	// Complete replaces the live pending entry under key whose Token is
	// token with done. It returns [ErrReservationLost] when there is no
	// such entry.
	Complete(ctx context.Context, key, token string, done Entry, now time.Time) error
	// Release deletes the live pending entry under key whose Token is
	// token. It returns [ErrReservationLost] when there is no such entry.
	Release(ctx context.Context, key, token string, now time.Time) error
}
