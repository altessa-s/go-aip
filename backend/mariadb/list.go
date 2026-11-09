// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb

import (
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

// ListEngineOption configures [NewListEngine]: the sqlbase.WithList… options.
type ListEngineOption = sqlbase.ListEngineOption

// NewListEngine constructs a keyset [listing] engine over a MariaDB table with [ListDialect] fixed: MariaDB
// syntax, storage validation, schema checks, index management and the filter translator in contract mode
// ([NewFilterTranslatorWithSchema]). It is [sqlbase.NewListEngine] without the dialect argument; embed
// [ListDialect] and call that one directly to override selected dialect behavior. dest returns Scan destinations
// in the same order as columns; a nil codec serves a single page only.
func NewListEngine[T any](q sqlbase.Querier, codec pagetoken.Signer, schema *listing.Schema, table string, columns []string,
	dest func(*T) []any, opts ...ListEngineOption,
) (*sqlbase.ListEngine[T], error) {
	return sqlbase.NewListEngine(q, codec, schema, table, columns, dest, ListDialect{}, opts...)
}
