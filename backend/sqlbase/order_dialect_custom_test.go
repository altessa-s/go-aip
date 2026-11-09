// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/metrics"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/projection"
)

// subsystemMeter records the subsystems a translator asks for.
type subsystemMeter struct {
	metrics.Meter

	subsystems *[]string
}

func (m subsystemMeter) WithSubsystem(subsystem string) metrics.Meter {
	*m.subsystems = append(*m.subsystems, subsystem)
	return m.Meter.WithSubsystem(subsystem)
}

func TestCustomOrderDialect(t *testing.T) {
	t.Parallel()

	ids := sqlbase.Identifiers{Quote: '"', MaxIdentLen: 8, MaxParts: 1}

	for _, tc := range []struct {
		name    string
		dialect sqlbase.OrderDialect
		key     orderby.Key
		want    string
		err     error
	}{
		{name: "plain", dialect: sqlbase.OrderDialect{Identifiers: ids}, key: orderby.Key{Name: "c"}, want: `"c" ASC`},
		{name: "collation unsupported", dialect: sqlbase.OrderDialect{Identifiers: ids}, key: orderby.Key{Name: "c", Collation: "C"},
			err: orderby.ErrUnsupportedOrdering},
		{name: "unknown collation style", dialect: sqlbase.OrderDialect{Identifiers: ids, Collation: 99},
			key: orderby.Key{Name: "c", Collation: "C"}, err: orderby.ErrUnsupportedOrdering},
		{name: "identifier collation", dialect: sqlbase.OrderDialect{Identifiers: ids, Collation: sqlbase.CollationIdentifier},
			key: orderby.Key{Name: "c", Collation: "C", Direction: orderby.DirectionDescending}, want: `"c" COLLATE "C" DESC`},
		{name: "nulls unsupported", dialect: sqlbase.OrderDialect{Identifiers: ids}, key: orderby.Key{Name: "c", Nulls: orderby.NullsFirst},
			err: orderby.ErrUnsupportedOrdering},
		{name: "unknown null placement", dialect: sqlbase.OrderDialect{Identifiers: ids, Nulls: 99},
			key: orderby.Key{Name: "c", Nulls: orderby.NullsLast}, err: orderby.ErrUnsupportedOrdering},
		{name: "nulls clause", dialect: sqlbase.OrderDialect{Identifiers: ids, Nulls: sqlbase.NullsClause},
			key: orderby.Key{Name: "c", Nulls: orderby.NullsLast}, want: `"c" ASC NULLS LAST`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			col, err := tc.dialect.Table(tc.key.Name)
			require.NoError(t, err)

			got, err := tc.dialect.RenderKey(col, tc.key)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	d := sqlbase.OrderDialect{Identifiers: ids}
	require.False(t, d.QualifiesNames())
	require.True(t, sqlbase.OrderDialect{Identifiers: ids, Qualified: true}.QualifiesNames())

	for _, name := range []string{"t.c", "toolongname"} {
		_, err := d.Table(name)
		require.Error(t, err, name)
	}

	_, err := sqlbase.Identifiers{}.Column("c")
	require.Error(t, err, "the zero Identifiers rejects every name")
}

func TestCustomProjectionDialectMetricsName(t *testing.T) {
	t.Parallel()

	policy, err := projection.NewPolicy(projection.WithAllowedFields("a"))
	require.NoError(t, err)

	ids := sqlbase.Identifiers{Quote: '"', MaxIdentLen: 63}

	for _, tc := range []struct {
		name, want string
	}{
		{name: "", want: "projection_sql"},
		{name: "duckdb", want: "projection_duckdb"},
	} {
		var subsystems []string

		tr, err := sqlbase.NewProjectionTranslator(policy, sqlbase.ProjectionDialect{Identifiers: ids, Name: tc.name},
			sqlbase.WithProjectionCollector(subsystemMeter{Meter: metrics.Noop(), subsystems: &subsystems}))
		require.NoError(t, err)
		require.Equal(t, []string{tc.want}, subsystems)

		cols, err := tr.Translate(projection.Spec{Paths: []string{"a"}})
		require.NoError(t, err)
		require.Equal(t, `"a"`, cols)
	}
}
