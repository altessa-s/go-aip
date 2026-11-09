// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken

//go:generate optgen generate

import "time"

const (
	// DefaultTTL is how long an issued token stays valid.
	DefaultTTL = 24 * time.Hour

	// DefaultMaxClockSkew is how far in the future a token's issue time may
	// lie, to tolerate clock differences between replicas.
	DefaultMaxClockSkew = time.Minute

	// MinKeyLength is the shortest signing key [New] accepts, in bytes.
	MinKeyLength = 32

	// MaxPayloadLength bounds the payload of a token, in bytes.
	MaxPayloadLength = 1024
)

// Clock returns the current time. Tests replace it; production uses
// [time.Now].
type Clock func() time.Time

// defaultClock is the production wall clock.
var defaultClock Clock = time.Now

type options struct {
	// maxPayloadLength bounds both issued and accepted payloads.
	maxPayloadLength int `optgen:"default=MaxPayloadLength" optval:"positive"`
	// ttl is how long an issued token stays valid.
	ttl time.Duration `optgen:"default=DefaultTTL"`
	// expiryDisabled makes tokens valid indefinitely, ignoring ttl.
	expiryDisabled bool `optval:"param"`
	// maxClockSkew bounds how far in the future an issue time may lie.
	maxClockSkew time.Duration `optgen:"default=DefaultMaxClockSkew"`
	// previousKeys verify tokens signed before a key rotation; tokens are
	// always issued with the current key.
	previousKeys [][]byte `optgen:"append"`
	// encryption issues encrypted tokens (AES-256-GCM under a key derived
	// from the signing key) whose payload and bindings the client cannot
	// read. Signed tokens are still accepted, so it can be enabled replica
	// by replica: every codec resolves encrypted tokens.
	encryption bool `optval:"param"`
	// strictEncryption issues encrypted tokens and rejects signed ones
	// (ErrUnencryptedToken): the final step of a rollout, once every signed
	// token in circulation has expired.
	strictEncryption bool `optval:"param"`
	// clock is the time source.
	clock Clock `optgen:"default=defaultClock"`
}
