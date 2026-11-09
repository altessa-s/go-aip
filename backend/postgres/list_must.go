// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

// MustNewListEngine is [sqlbase.MustNewListEngine] with [ListDialect] fixed, for package-level declarations with static
// configuration: it returns the engine [NewListEngine] builds and panics on an invalid configuration with an error prefixed
// with "sqlbase.MustNewListEngine: " that wraps the NewListEngine error, so errors.Is on the recovered value matches whatever NewListEngine
// returned ([listing.ErrInvalidSchema] for an invalid schema). Use NewListEngine where the configuration comes from input rather than code.
func MustNewListEngine[T any](q sqlbase.Querier, codec pagetoken.Signer, schema *listing.Schema, table string, columns []string,
	dest func(*T) []any, opts ...ListEngineOption,
) *sqlbase.ListEngine[T] {
	return sqlbase.MustNewListEngine(q, codec, schema, table, columns, dest, ListDialect{}, opts...)
}
