// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/metrics"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/projection"
)

// TestTranslatorMetricNames pins the per-dialect metric names of the SQL order and projection translators.
func TestTranslatorMetricNames(t *testing.T) {
	t.Parallel()

	custom := postgres.OrderDialect()
	custom.Name = ""
	customProjection := postgres.ProjectionDialect()
	customProjection.Name = ""

	tests := []struct {
		name       string
		order      sqlbase.OrderDialect
		projection sqlbase.ProjectionDialect
		label      string
	}{
		{"postgres", postgres.OrderDialect(), postgres.ProjectionDialect(), "postgres"},
		{"mariadb", mariadb.OrderDialect(), mariadb.ProjectionDialect(), "mariadb"},
		{"clickhouse", clickhouse.OrderDialect(), clickhouse.ProjectionDialect(), "clickhouse"},
		{"unnamed", custom, customProjection, "sql"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newCountingMeter()

			op, err := orderby.NewPolicy(orderby.WithAllowedFields("name"))
			require.NoError(t, err)
			ot, err := sqlbase.NewOrderTranslator(op, tt.order, sqlbase.WithOrderCollector(m))
			require.NoError(t, err)
			_, err = ot.Translate(orderby.Spec{Keys: []orderby.Key{{Name: "name"}}})
			require.NoError(t, err)

			pp, err := projection.NewPolicy(projection.WithAllowedFields("name"))
			require.NoError(t, err)
			pt, err := sqlbase.NewProjectionTranslator(pp, tt.projection, sqlbase.WithProjectionCollector(m))
			require.NoError(t, err)
			_, err = pt.Translate(projection.Spec{Paths: []string{"name"}})
			require.NoError(t, err)

			require.Equal(t, map[string]int{
				"orderby_" + tt.label + "_translate_duration_seconds":    1,
				"projection_" + tt.label + "_translate_duration_seconds": 1,
			}, m.Counts())
		})
	}
}

// namedDialect labels the metrics of the failing test dialect.
type namedDialect struct {
	failing

	name string
}

func (d namedDialect) Name() string { return d.name }

// TestFilterTranslatorMetricNames pins the filter metric names: the dialect wrappers and sqlbase.WithFilterCollector record
// filter_<name>_translate_duration_seconds once per Translate, TranslateInline and ValidateAST call, "sql" for an unnamed
// dialect.
func TestFilterTranslatorMetricNames(t *testing.T) {
	t.Parallel()

	type translator interface {
		Translate(filter.Node) (string, []any, error)
		TranslateInline(filter.Node) (string, error)
		ValidateAST(filter.Node) error
	}

	policy, err := filter.NewPolicy(filter.WithAllowedFields("name"))
	require.NoError(t, err)

	tests := []struct {
		name  string
		build func(metrics.Meter) (translator, error)
		label string
	}{
		{"postgres", func(m metrics.Meter) (translator, error) {
			return postgres.NewFilterTranslator(policy, postgres.WithFilterCollector(m))
		}, "postgres"},
		{"mariadb", func(m metrics.Meter) (translator, error) {
			return mariadb.NewFilterTranslator(policy, mariadb.WithFilterCollector(m))
		}, "mariadb"},
		{"clickhouse", func(m metrics.Meter) (translator, error) {
			return clickhouse.NewFilterTranslator(policy, clickhouse.WithFilterCollector(m))
		}, "clickhouse"},
		{"named", func(m metrics.Meter) (translator, error) {
			return sqlbase.NewFilterTranslator(policy, namedDialect{name: "custom"}, sqlbase.WithFilterCollector(m))
		}, "custom"},
		{"empty name", func(m metrics.Meter) (translator, error) {
			return sqlbase.NewFilterTranslator(policy, namedDialect{}, sqlbase.WithFilterCollector(m))
		}, "sql"},
		{"unnamed", func(m metrics.Meter) (translator, error) {
			return sqlbase.NewFilterTranslator(policy, failing{}, sqlbase.WithFilterCollector(m))
		}, "sql"},
	}

	node := &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "name"}, Right: &filter.LiteralNode{Value: "a"}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newCountingMeter()
			tr, err := tt.build(m)
			require.NoError(t, err)

			_, _, err = tr.Translate(node)
			require.NoError(t, err)
			_, err = tr.TranslateInline(node)
			require.NoError(t, err)
			require.NoError(t, tr.ValidateAST(node))

			require.Equal(t, map[string]int{"filter_" + tt.label + "_translate_duration_seconds": 3}, m.Counts())

			_, isVisitor := tr.(filter.Visitor)
			require.False(t, isVisitor, "the walker's visit methods are not public surface")
		})
	}
}
