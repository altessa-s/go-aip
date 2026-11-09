// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
)

// The built-in dialects' column-reference grammars.
var (
	pgIdentifiers         = sqlbase.Identifiers{Quote: '"', MaxIdentLen: sqlbase.PostgresMaxIdentLen, MaxParts: 2}
	mariadbIdentifiers    = sqlbase.Identifiers{Quote: '`', MaxIdentLen: sqlbase.MariaDBMaxIdentLen, MaxParts: 2}
	clickhouseIdentifiers = sqlbase.Identifiers{Quote: '`', MaxIdentLen: sqlbase.ClickHouseMaxIdentLen}
)

func TestIdentifiersColumn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		columns sqlbase.Identifiers
		in      string
		want    string
	}{
		{name: "postgres column", columns: pgIdentifiers, in: "a", want: `"a"`},
		{name: "postgres qualified", columns: pgIdentifiers, in: "t.a", want: `"t"."a"`},
		{name: "postgres three parts", columns: pgIdentifiers, in: "s.t.a"},
		{name: "postgres boundary", columns: pgIdentifiers, in: strings.Repeat("a", 63), want: `"` + strings.Repeat("a", 63) + `"`},
		{name: "postgres too long", columns: pgIdentifiers, in: strings.Repeat("a", 64)},
		{name: "mariadb qualified", columns: mariadbIdentifiers, in: "t.a", want: "`t`.`a`"},
		{name: "mariadb three parts", columns: mariadbIdentifiers, in: "d.t.a"},
		{name: "mariadb boundary", columns: mariadbIdentifiers, in: strings.Repeat("a", 64), want: "`" + strings.Repeat("a", 64) + "`"},
		{name: "clickhouse nested parts", columns: clickhouseIdentifiers, in: "a.b.c", want: "`a`.`b`.`c`"},
		{name: "mariadb too long", columns: mariadbIdentifiers, in: "t." + strings.Repeat("a", 65)},
		{name: "clickhouse boundary", columns: clickhouseIdentifiers, in: strings.Repeat("a", 255), want: "`" + strings.Repeat("a", 255) + "`"},
		{name: "clickhouse too long", columns: clickhouseIdentifiers, in: strings.Repeat("a", 256)},
		{name: "empty", columns: mariadbIdentifiers, in: ""},
		{name: "empty part", columns: mariadbIdentifiers, in: "t..a"},
		{name: "leading digit", columns: mariadbIdentifiers, in: "1a"},
		{name: "quote", columns: mariadbIdentifiers, in: "a`b"},
		{name: "expression", columns: pgIdentifiers, in: "lower(a)"},
		{name: "no quote", columns: sqlbase.Identifiers{}, in: "a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := tc.columns.Column(tc.in)
			if tc.want == "" {
				require.ErrorIs(t, err, sqlbase.ErrInvalidColumnName)
				require.Empty(t, got)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
