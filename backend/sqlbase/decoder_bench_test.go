// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"database/sql"
	"testing"

	"github.com/altessa-s/go-aip/backend/sqlbase"
)

func BenchmarkDecode(b *testing.B) {
	v := sql.NullInt64{Int64: 7, Valid: true}

	d := sqlbase.Decoder{}
	for b.Loop() {
		_, _ = d.Decode(v)
	}
}
