// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
)

func BenchmarkField(b *testing.B) {
	schema, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar("account.name", sqlbase.Text)})
	require.NoError(b, err)
	b.ReportAllocs()

	for b.Loop() {
		_, _ = schema.Field("name")
	}
}
