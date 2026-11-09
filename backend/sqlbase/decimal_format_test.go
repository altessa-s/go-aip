// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
)

func TestDecimalFormat(t *testing.T) {
	t.Parallel()

	f := sqlbase.DecimalFormat{Precision: 38, Scale: 35}
	sql, err := f.SQL()
	require.NoError(t, err)
	require.Equal(t, "DECIMAL(38,35)", sql)
	require.NoError(t, f.Check("0.12345678901234567890123456789012345"))
	require.ErrorIs(t, f.Check("0.123456789012345678901234567890123456"), filter.ErrFieldTypeMismatch)
	require.ErrorIs(t, f.Check("1000"), filter.ErrFieldTypeMismatch)
	require.ErrorIs(t, (sqlbase.DecimalFormat{Precision: 66}).Validate(), filter.ErrInvalidOptions)
}
