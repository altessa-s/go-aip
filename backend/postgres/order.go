// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/orderby"
)

// dialectName labels the metrics of every translator of the package, as in filter_postgres_translate_duration_seconds.
const dialectName = "postgres"

// qualifiedParts admits `col` and `table.col` column references.
const qualifiedParts = 2

// identifiers is the PostgreSQL column-reference grammar: `col` or `table.col`, each part at most 63 bytes, because
// PostgreSQL silently truncates longer identifiers.
var identifiers = sqlbase.Identifiers{Quote: '"', MaxIdentLen: sqlbase.PostgresMaxIdentLen, MaxParts: qualifiedParts}

// OrderDialect returns the PostgreSQL ORDER BY dialect: double-quoted `col` or `table.col` parts of at most 63 bytes,
// `COLLATE "name"` before the direction, NULLS FIRST/LAST, and metrics labeled "postgres". A dotted client key must be declared
// ([sqlbase.QualifiedNamesDialect]). Pass it to [sqlbase.NewOrderTranslator] or delegate an application
// [sqlbase.ColumnResolver] to it.
func OrderDialect() sqlbase.OrderDialect {
	return sqlbase.OrderDialect{
		Name: dialectName, Identifiers: identifiers, Collation: sqlbase.CollationIdentifier,
		Nulls: sqlbase.NullsClause, Qualified: true,
	}
}

// NewOrderTranslator creates an ORDER BY translator for PostgreSQL bound to
// policy, the client-facing [orderby.Policy] shared by every backend of the
// resource. It is [sqlbase.NewOrderTranslator] with [OrderDialect] fixed:
// double-quoted column parts of at most 63 bytes, `COLLATE "name"` before the
// direction and NULLS FIRST/LAST. [sqlbase.WithOrderOptions] binds the policy
// to storage names and [sqlbase.WithOrderCollector] records metrics. Returns
// [orderby.ErrPolicyRequired] for a nil policy.
func NewOrderTranslator(policy *orderby.Policy, opts ...sqlbase.OrderOption) (*sqlbase.OrderTranslator, error) {
	return sqlbase.NewOrderTranslator(policy, OrderDialect(), opts...)
}
