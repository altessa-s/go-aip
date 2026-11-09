// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetokentest

import (
	"bytes"
	"testing"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

// Key is the fixed signing key of [Codec] and [MustCodec]; OtherKey is a
// second fixed key, so a test can sign tokens that a [Codec] rejects unless it
// lists OtherKey with pagetoken.WithPreviousKeys. Both are [pagetoken.MinKeyLength]
// bytes long. Each call returns independently owned bytes.
func Key() []byte { return bytes.Repeat([]byte{'k'}, pagetoken.MinKeyLength) }

// OtherKey returns a fresh key distinct from Key.
func OtherKey() []byte { return bytes.Repeat([]byte{'o'}, pagetoken.MinKeyLength) }

// Codec returns a codec signing with [Key]; opts tune it (TTL, clock,
// previous keys). Invalid opts fail tb.
func Codec(tb testing.TB, opts ...pagetoken.Option) *pagetoken.Codec {
	tb.Helper()
	return CodecWithKey(tb, Key(), opts...)
}

// MustCodec returns a codec signing with [Key] for package-level test
// doubles, where no testing.TB is at hand. It panics if opts are invalid.
func MustCodec(opts ...pagetoken.Option) *pagetoken.Codec {
	c, err := pagetoken.New(Key(), opts...)
	if err != nil {
		panic("pagetokentest: " + err.Error())
	}

	return c
}

// CodecWithKey returns a codec signing with key, for example [OtherKey]. A
// key shorter than [pagetoken.MinKeyLength] or invalid opts fail tb.
func CodecWithKey(tb testing.TB, key []byte, opts ...pagetoken.Option) *pagetoken.Codec {
	tb.Helper()

	c, err := pagetoken.New(key, opts...)
	if err != nil {
		tb.Fatalf("pagetoken.New: %v", err)
	}

	return c
}
