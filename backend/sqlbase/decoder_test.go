// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
)

var errValue = errors.New("invalid SQL value")

type failingValuer struct{}

func (failingValuer) Value() (driver.Value, error) { return nil, errValue }
func TestDecoder(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		value, want any
	}{
		{"valid", sql.NullString{String: "x", Valid: true}, "x"}, {"invalid", sql.NullInt64{}, nil},
		{"generic", sql.Null[int64]{V: 7, Valid: true}, int64(7)}, {"native", true, true},
		{"nil pointer", (*sql.NullString)(nil), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := (sqlbase.Decoder{}).Decode(tc.value)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	_, err := (sqlbase.Decoder{}).Decode(failingValuer{})
	require.ErrorIs(t, err, errValue)
}
