// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken_test

import (
	"bytes"
	"encoding/gob"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

type gobPayload struct{}

func (gobPayload) Marshal(v any) ([]byte, error) {
	var b bytes.Buffer

	err := gob.NewEncoder(&b).Encode(v)

	return b.Bytes(), err
}

func (gobPayload) Unmarshal(b []byte, v any) error {
	return gob.NewDecoder(bytes.NewReader(b)).Decode(v)
}

func TestPayloadCodec(t *testing.T) {
	t.Parallel()
	typed := pagetoken.NewTyped[[]int](pagetokentest.Codec(t), pagetoken.WithPayloadCodec(gobPayload{}))
	binding := pagetoken.NewBindings("list", "binary-v1")
	token, err := typed.Issue([]int{1, 2, 3}, binding)
	require.NoError(t, err)
	got, first, err := typed.Resolve(token, binding)
	require.NoError(t, err)
	require.False(t, first)
	require.Equal(t, []int{1, 2, 3}, got)
}

func TestConfigurablePayloadLimit(t *testing.T) {
	t.Parallel()

	for _, encrypted := range []bool{false, true} {
		t.Run(map[bool]string{false: "signed", true: "encrypted"}[encrypted], func(t *testing.T) {
			t.Parallel()

			opts := []pagetoken.Option{pagetoken.WithMaxPayloadLength(4096)}
			if encrypted {
				opts = append(opts, pagetoken.WithEncryption(true))
			}

			large := pagetokentest.Codec(t, opts...)
			small := pagetokentest.Codec(t)
			payload := bytes.Repeat([]byte("x"), 4096)
			token, err := large.Issue(payload, bindings)
			require.NoError(t, err)
			got, err := large.Resolve(token, bindings)
			require.NoError(t, err)
			require.Equal(t, payload, got)

			_, err = small.Resolve(token, bindings)
			require.ErrorIs(t, err, pagetoken.ErrInvalidToken)
			_, err = large.Issue(append(payload, 'x'), bindings)
			require.ErrorIs(t, err, pagetoken.ErrPayloadTooLarge)
		})
	}
}
