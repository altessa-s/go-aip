// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package pagetoken creates and verifies protected, stateless continuation (page) tokens.
//
// New(key, opts...) constructs a Codec implementing Signer. Tokens are stateless, authenticated with HMAC-SHA256, expire after 24 hours by
// default, and tolerate one minute of future clock skew. Keys are at least 32 bytes; payloads default to at most 1,024 bytes. WithClock
// supplies an instance clock for deterministic tests. WithExpiryDisabled(true) explicitly removes expiry.
//
// # Usage
//
//	codec, err := pagetoken.New(signingKey)
//	token, err := codec.Issue([]byte(position), pagetoken.Bindings{Sort: sortFP, Filter: filterFP, Subject: user})
//	position, err := codec.Resolve(token, pagetoken.Bindings{Sort: sortFP, Filter: filterFP, Subject: user})
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package pagetoken
