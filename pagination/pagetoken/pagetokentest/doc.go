// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package pagetokentest provides page-token codecs with fixed signing keys for testing code built on
// github.com/altessa-s/go-aip/pagination/pagetoken.
//
// Fixed-key constructors are for tests only. They create reproducible signed or encrypted codecs and rotation fixtures; production services
// supply their own keys to pagetoken.New. Clock-sensitive tests can configure the clock through ordinary codec options.
//
// # Usage
//
//	codec := pagetokentest.Codec(t, pagetoken.WithClock(fake.Now))
//	foreign := pagetokentest.CodecWithKey(t, pagetokentest.OtherKey())
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package pagetokentest
