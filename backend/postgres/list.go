// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

// NewListEngine constructs a keyset list engine over a PostgreSQL table. It is
// [sqlbase.NewListEngine] with [ListDialect] fixed: PostgreSQL syntax, storage
// validation, schema checks and index management, and the schema filter
// translator in listing contract mode. dest returns Scan destinations in the
// same order as columns. A nil codec serves a single page only. The
// sqlbase WithList… options configure columns, scope keys, identity, precision
// and limits.
func NewListEngine[T any](q sqlbase.Querier, codec pagetoken.Signer, schema *listing.Schema, table string, columns []string,
	dest func(*T) []any, opts ...sqlbase.ListEngineOption,
) (*sqlbase.ListEngine[T], error) {
	return sqlbase.NewListEngine(q, codec, schema, table, columns, dest, ListDialect{}, opts...)
}
