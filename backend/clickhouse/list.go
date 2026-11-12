// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse

import (
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

// NewListEngine constructs a keyset [listing] engine over a ClickHouse table with [ListDialect] fixed: backtick
// identifiers, `?` placeholders, typed cursor operands, system.columns schema checks, minmax skipping indexes and the
// filter translator in contract mode ([NewFilterTranslatorWithSchema]). It is [sqlbase.NewListEngine] without the
// dialect argument; embed [ListDialect] and call that one directly to override selected dialect behavior. dest
// returns Scan destinations in the same order as columns; a nil codec serves a single page only.
func NewListEngine[T any](q sqlbase.Querier, codec pagetoken.Signer, schema *listing.Schema, table string, columns []string,
	dest func(*T) []any, opts ...sqlbase.ListEngineOption,
) (*sqlbase.ListEngine[T], error) {
	return sqlbase.NewListEngine(q, codec, schema, table, columns, dest, ListDialect{}, opts...)
}
