// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetokentest_test

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

var bindings = pagetoken.NewBindings("list", "name:1")

func TestKeys(t *testing.T) {
	t.Parallel()

	require.Len(t, pagetokentest.Key(), pagetoken.MinKeyLength)
	require.Len(t, pagetokentest.OtherKey(), pagetoken.MinKeyLength)
	require.False(t, bytes.Equal(pagetokentest.Key(), pagetokentest.OtherKey()))
}

func TestCodecRoundTrip(t *testing.T) {
	t.Parallel()

	for name, codec := range map[string]*pagetoken.Codec{
		"Codec":        pagetokentest.Codec(t),
		"MustCodec":    pagetokentest.MustCodec(),
		"CodecWithKey": pagetokentest.CodecWithKey(t, pagetokentest.Key()),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			token, err := codec.Issue([]byte("cursor"), bindings)
			require.NoError(t, err)
			// Every codec on Key accepts the tokens of the others.
			got, err := pagetokentest.Codec(t).Resolve(token, bindings)
			require.NoError(t, err)
			require.Equal(t, []byte("cursor"), got)
		})
	}
}

func TestOtherKeyIsForeign(t *testing.T) {
	t.Parallel()

	token, err := pagetokentest.CodecWithKey(t, pagetokentest.OtherKey()).Issue([]byte("cursor"), bindings)
	require.NoError(t, err)

	_, err = pagetokentest.Codec(t).Resolve(token, bindings)
	require.Equal(t, pagetoken.FailureMalformed, pagetoken.Classify(err))

	rotated := pagetokentest.Codec(t, pagetoken.WithPreviousKeys(pagetokentest.OtherKey()))
	got, err := rotated.Resolve(token, bindings)
	require.NoError(t, err)
	require.Equal(t, []byte("cursor"), got)
}

func TestCodecOptions(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	codec := pagetokentest.Codec(t, pagetoken.WithTTL(time.Minute), pagetoken.WithClock(clock))
	token, err := codec.Issue([]byte("cursor"), bindings)
	require.NoError(t, err)

	later := pagetokentest.Codec(t, pagetoken.WithTTL(time.Minute), pagetoken.WithClock(func() time.Time { return now.Add(time.Hour) }))
	_, err = later.Resolve(token, bindings)
	require.Equal(t, pagetoken.FailureExpired, pagetoken.Classify(err))
}

// fatalTB records a Fatalf instead of stopping the test.
type fatalTB struct {
	testing.TB
	msg string
}

func (f *fatalTB) Helper() {}

func (f *fatalTB) Fatalf(format string, args ...any) { f.msg = fmt.Sprintf(format, args...) }

func TestInvalidKeyFailsTB(t *testing.T) {
	t.Parallel()

	tb := &fatalTB{TB: t}
	require.Nil(t, pagetokentest.CodecWithKey(tb, []byte("short")))
	require.Contains(t, tb.msg, "pagetoken.New")
}

func TestMustCodecPanicsOnInvalidOption(t *testing.T) {
	t.Parallel()

	require.Panics(t, func() { pagetokentest.MustCodec(pagetoken.WithPreviousKeys([]byte("short"))) })
}
