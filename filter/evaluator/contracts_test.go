// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter/evaluator"
)

type identityDecoder struct{}

func (identityDecoder) Decode(value any) (any, error) { return value, nil }
func TestValueDecoder(t *testing.T) {
	t.Parallel()

	var d evaluator.ValueDecoder = identityDecoder{}

	v, err := d.Decode(1)
	require.NoError(t, err)
	require.Equal(t, 1, v)
}
