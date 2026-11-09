// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse

import (
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/orderby"
)

// dialectName labels the metrics of every translator of the package, as in filter_clickhouse_translate_duration_seconds.
const dialectName = "clickhouse"

// identifiers is the ClickHouse column-reference grammar: any number of parts of at most 255 bytes, so Nested and JSON
// subcolumns can be addressed part by part.
var identifiers = sqlbase.Identifiers{Quote: '`', MaxIdentLen: sqlbase.ClickHouseMaxIdentLen}

// OrderOption configures [NewOrderTranslator]. It is [sqlbase.OrderOption]; the
// options are [sqlbase.WithOrderOptions] and [sqlbase.WithOrderCollector].
type OrderOption = sqlbase.OrderOption

// OrderDialect returns the ClickHouse ORDER BY dialect: backtick-quoted parts of at most 255 bytes, NULLS FIRST/LAST,
// and `COLLATE 'locale'` after them. The locale is a string literal there, so it must be a plain locale identifier
// (letters, digits, '_' and '-', starting with a letter, at most 64 bytes). A dot addresses a Nested or JSON subcolumn part
// by part, or a joined table, so a dotted client key must be declared ([sqlbase.QualifiedNamesDialect]): one a client composed
// under a wildcard allow-list is refused, as in PostgreSQL and MariaDB. Metrics are labeled "clickhouse". Pass it to
// [sqlbase.NewOrderTranslator] or delegate an application [sqlbase.ColumnResolver] to it.
func OrderDialect() sqlbase.OrderDialect {
	return sqlbase.OrderDialect{
		Name: dialectName, Identifiers: identifiers, Collation: sqlbase.CollationLiteral, Nulls: sqlbase.NullsClause,
		Qualified: true,
	}
}

// NewOrderTranslator creates an ORDER BY translator for ClickHouse bound to
// policy, the client-facing [orderby.Policy] shared by every backend of the
// resource. It is [sqlbase.NewOrderTranslator] with [OrderDialect] fixed:
// backtick-quoted column parts of at most 255 bytes, NULLS FIRST/LAST and
// `COLLATE 'locale'`. [sqlbase.WithOrderOptions] binds the policy to storage
// names and [sqlbase.WithOrderCollector] records metrics. Returns
// [orderby.ErrPolicyRequired] for a nil policy.
func NewOrderTranslator(policy *orderby.Policy, opts ...OrderOption) (*sqlbase.OrderTranslator, error) {
	return sqlbase.NewOrderTranslator(policy, OrderDialect(), opts...)
}
