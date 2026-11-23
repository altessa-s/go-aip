// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"database/sql/driver"
	"testing"

	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/listing"
)

func BenchmarkCount(b *testing.B) {
	db, fake := sqlbasetest.NewFakeDB(b, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"count"}, Rows: [][]driver.Value{{int64(10)}}}
	})
	fake.DisableRecording()

	engine := newCountEngine(b, db)
	req := listing.Request{Filter: `id != "other"`}

	b.ReportAllocs()

	for b.Loop() {
		if _, err := engine.Count(b.Context(), req, nil); err != nil {
			b.Fatal(err)
		}
	}
}
