// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken_test

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

// secretPayload is a payload whose bytes must never appear in an encrypted
// token.
var secretPayload = []byte("salary=123456|01JABCDEFGHJKMNPQRSTVWXYZ00")

// rawToken decodes a token to its bytes.
func rawToken(t *testing.T, token string) []byte {
	t.Helper()

	raw, err := base64.RawURLEncoding.DecodeString(token)
	require.NoError(t, err)

	return raw
}

// TestEncryption_HidesPayload checks an encrypted token neither shows its
// payload nor its bindings, and that each issue draws a fresh nonce.
func TestEncryption_HidesPayload(t *testing.T) {
	t.Parallel()

	c := newCodec(t, pagetoken.WithEncryption(true))
	token, err := c.Issue(secretPayload, bindings)
	require.NoError(t, err)
	require.NotContains(t, string(rawToken(t, token)), string(secretPayload))

	again, err := c.Issue(secretPayload, bindings)
	require.NoError(t, err)
	require.NotEqual(t, token, again, "a nonce is drawn per token")

	payload, err := c.Resolve(token, bindings)
	require.NoError(t, err)
	require.Equal(t, secretPayload, payload)

	signed, err := newCodec(t).Issue(secretPayload, bindings)
	require.NoError(t, err)
	require.Contains(t, string(rawToken(t, signed)), string(secretPayload), "a signed token is readable")
}

// TestEncryption_RejectsTamperingAndForeignKeys flips every bit position of
// an encrypted token and resolves it with a codec under another key.
func TestEncryption_RejectsTamperingAndForeignKeys(t *testing.T) {
	t.Parallel()

	c := newCodec(t, pagetoken.WithEncryption(true))
	token, err := c.Issue([]byte("payload"), bindings)
	require.NoError(t, err)

	raw := rawToken(t, token)
	for i := range raw {
		tampered := bytes.Clone(raw)
		tampered[i] ^= 0x01
		_, err := c.Resolve(base64.RawURLEncoding.EncodeToString(tampered), bindings)
		require.ErrorIs(t, err, pagetoken.ErrInvalidToken, "flipping byte %d must invalidate the token", i)
	}

	for _, n := range []int{1, 9, 21, len(raw) - 1} {
		_, err := c.Resolve(base64.RawURLEncoding.EncodeToString(raw[:n]), bindings)
		require.ErrorIs(t, err, pagetoken.ErrInvalidToken, "truncated to %d bytes", n)
	}

	foreign, err := pagetoken.New(otherKey, pagetoken.WithEncryption(true))
	require.NoError(t, err)
	_, err = foreign.Resolve(token, pagetoken.Bindings{Sort: "other"})
	require.ErrorIs(t, err, pagetoken.ErrInvalidToken, "a foreign key fails before the bindings")
}

// TestEncryption_KeyRotation checks a token encrypted under a retired key
// still resolves while the key is a previous key, and not once it is
// dropped.
func TestEncryption_KeyRotation(t *testing.T) {
	t.Parallel()

	old, err := pagetoken.New(otherKey, pagetoken.WithEncryption(true))
	require.NoError(t, err)
	token, err := old.Issue([]byte("p"), bindings)
	require.NoError(t, err)

	payload, err := newCodec(t, pagetoken.WithEncryption(true), pagetoken.WithPreviousKeys(otherKey)).Resolve(token, bindings)
	require.NoError(t, err)
	require.Equal(t, []byte("p"), payload)

	_, err = newCodec(t, pagetoken.WithEncryption(true)).Resolve(token, bindings)
	require.ErrorIs(t, err, pagetoken.ErrInvalidToken)
}

// TestEncryption_ExpiryAndBindings checks the TTL and the bindings of an
// encrypted token behave as for a signed one.
func TestEncryption_ExpiryAndBindings(t *testing.T) {
	t.Parallel()

	issuedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	issuer := newCodec(t, pagetoken.WithEncryption(true), pagetoken.WithClock(fixedClock(issuedAt)))
	token, err := issuer.Issue([]byte("p"), bindings)
	require.NoError(t, err)

	late := newCodec(t, pagetoken.WithEncryption(true), pagetoken.WithClock(fixedClock(issuedAt.Add(pagetoken.DefaultTTL+time.Second))))
	_, err = late.Resolve(token, bindings)
	require.ErrorIs(t, err, pagetoken.ErrExpiredToken)

	early := newCodec(t, pagetoken.WithEncryption(true), pagetoken.WithClock(fixedClock(issuedAt.Add(-2*pagetoken.DefaultMaxClockSkew))))
	_, err = early.Resolve(token, bindings)
	require.ErrorIs(t, err, pagetoken.ErrInvalidToken, "an issue time in the future")

	for want, b := range map[error]pagetoken.Bindings{
		pagetoken.ErrSortChanged:     {Sort: "ts:asc", Filter: bindings.Filter, Subject: bindings.Subject},
		pagetoken.ErrFilterChanged:   {Sort: bindings.Sort, Filter: "actor=b", Subject: bindings.Subject},
		pagetoken.ErrSubjectMismatch: {Sort: bindings.Sort, Filter: bindings.Filter, Subject: "user-2"},
	} {
		_, err = issuer.Resolve(token, b)
		require.ErrorIs(t, err, want)
	}
}

// TestEncryption_Transition checks the rollout: every codec resolves both
// formats — so encryption can be enabled replica by replica — until strict
// mode rejects signed tokens.
func TestEncryption_Transition(t *testing.T) {
	t.Parallel()

	signed, err := newCodec(t).Issue([]byte("v1"), bindings)
	require.NoError(t, err)
	encrypted, err := newCodec(t, pagetoken.WithEncryption(true)).Issue([]byte("v2"), bindings)
	require.NoError(t, err)

	for name, c := range map[string]*pagetoken.Codec{
		"plain":     newCodec(t),
		"encrypted": newCodec(t, pagetoken.WithEncryption(true)),
	} {
		payload, err := c.Resolve(signed, bindings)
		require.NoError(t, err, name)
		require.Equal(t, []byte("v1"), payload)
		payload, err = c.Resolve(encrypted, bindings)
		require.NoError(t, err, name)
		require.Equal(t, []byte("v2"), payload)
	}

	strict := newCodec(t, pagetoken.WithStrictEncryption(true))
	_, err = strict.Resolve(signed, bindings)
	require.ErrorIs(t, err, pagetoken.ErrInvalidToken)
	require.ErrorIs(t, err, pagetoken.ErrUnencryptedToken)
	require.Equal(t, pagetoken.FailureMalformed, pagetoken.Classify(err))
	payload, err := strict.Resolve(encrypted, bindings)
	require.NoError(t, err)
	require.Equal(t, []byte("v2"), payload)

	token, err := strict.Issue(secretPayload, bindings)
	require.NoError(t, err)
	require.NotContains(t, string(rawToken(t, token)), string(secretPayload), "strict mode issues encrypted tokens")
}

// TestEncryption_LargePayload checks the largest payload round-trips.
func TestEncryption_LargePayload(t *testing.T) {
	t.Parallel()

	c := newCodec(t, pagetoken.WithStrictEncryption(true))
	payload := bytes.Repeat([]byte{0xff}, pagetoken.MaxPayloadLength)
	token, err := c.Issue(payload, bindings)
	require.NoError(t, err)
	got, err := c.Resolve(token, bindings)
	require.NoError(t, err)
	require.Equal(t, payload, got)
}
