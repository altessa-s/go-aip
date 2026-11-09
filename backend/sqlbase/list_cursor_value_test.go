// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

func TestCustomCursorValue(t *testing.T) {
	t.Parallel()

	var stored any = "domain-value"
	require.Equal(t, stored, deref(&stored))
	value, err := decodeSortValue(pagetoken.OpaqueSortValue(stored), Text)
	require.NoError(t, err)
	require.Equal(t, stored, value)

	_, err = decodeSortValue(pagetoken.OpaqueSortValue(struct{}{}), Text)
	require.ErrorIs(t, err, pagetoken.ErrInvalidToken)
}
