// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb

import (
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/orderby"
)

// dialectName labels the metrics of every translator of the package, as in filter_mariadb_translate_duration_seconds.
const dialectName = "mariadb"

// qualifiedParts admits `col` and `table.col` column references.
const qualifiedParts = 2

// identifiers is the MariaDB column-reference grammar: `col` or `table.col`, each part at most 64 bytes, the MariaDB and
// MySQL identifier limit. A third part would name the database, as the filter dialect refuses too.
var identifiers = sqlbase.Identifiers{Quote: '`', MaxIdentLen: sqlbase.MariaDBMaxIdentLen, MaxParts: qualifiedParts}

// OrderDialect returns the MariaDB and MySQL ORDER BY dialect, with metrics labeled "mariadb": backtick-quoted `col` or `table.col` parts of at most 64
// bytes; a dotted client key must be declared ([sqlbase.QualifiedNamesDialect]). A collation is written unquoted
// before the direction (`col COLLATE utf8mb4_bin DESC`), so it must be a plain collation name (letters, digits and
// '_', starting with a letter, at most 64 bytes). MySQL has no NULLS FIRST/LAST and orders null before every value: a
// placement equal to that native order (asc nulls first, desc nulls last) renders as a plain key, and the opposite
// one adds a leading `col IS NULL` key: `col IS NULL ASC, col ASC` for asc nulls last and `col IS NULL DESC, col DESC`
// for desc nulls first. Pass it to [sqlbase.NewOrderTranslator] or delegate an application [sqlbase.ColumnResolver]
// to it.
func OrderDialect() sqlbase.OrderDialect {
	return sqlbase.OrderDialect{
		Name: dialectName, Identifiers: identifiers, Collation: sqlbase.CollationBare,
		Nulls: sqlbase.NullsSmallestEmulated, Qualified: true,
	}
}

// NewOrderTranslator creates an ORDER BY translator with the MariaDB dialect fixed ([OrderDialect]):
// backtick-quoted columns, NULL placement emulated with an IS NULL sort key and COLLATE for collated keys. It is
// [sqlbase.NewOrderTranslator] without the dialect argument; use that one directly for an application
// [sqlbase.ColumnResolver]. Returns [orderby.ErrPolicyRequired] for a nil policy.
func NewOrderTranslator(policy *orderby.Policy, opts ...sqlbase.OrderOption) (*sqlbase.OrderTranslator, error) {
	return sqlbase.NewOrderTranslator(policy, OrderDialect(), opts...)
}
