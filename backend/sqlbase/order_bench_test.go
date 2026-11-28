// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

func BenchmarkTranslateOrder(b *testing.B) {
	cfg := orderContext(b, orderbytest.MustPolicy(b),
		orderby.WithFieldMapping(map[string]string{"address.city": "address.city"}))
	ob := orderbytest.MustParseOrderBy(b, "create_time desc, organization_id, slug, address.city desc, updated_at")
	b.ReportAllocs()

	for b.Loop() {
		_, err := sqlbase.TranslateOrder(cfg, postgres.OrderDialect(), ob)
		require.NoError(b, err)
	}
}

func BenchmarkOrderTranslator_Dialects(b *testing.B) {
	ob := orderbytest.MustParseOrderBy(b, "create_time desc, organization_id asc, slug, address.city desc, updated_at")
	for _, d := range orderDialects() {
		b.Run(d.name, func(b *testing.B) {
			tr := mustOrderTranslator(b, orderbytest.MustPolicy(b), d.dialect,
				orderby.WithFieldMapping(map[string]string{"address.city": "address.city"}))
			_, err := tr.Translate(ob)
			require.NoError(b, err)
			b.ReportAllocs()

			for b.Loop() {
				_, _ = tr.Translate(ob)
			}
		})
	}
}

func BenchmarkTranslator_Attributes(b *testing.B) {
	spec := orderby.Spec{Keys: []orderby.Key{
		{Name: "name", Direction: orderby.DirectionDescending, Nulls: orderby.NullsLast, Collation: "en_US"},
		{Name: "id"},
	}}
	tr := mustOrderTranslator(b, orderbytest.MustPolicy(b), clickhouse.OrderDialect())
	b.ReportAllocs()

	for b.Loop() {
		_, _ = tr.Translate(spec)
	}
}
