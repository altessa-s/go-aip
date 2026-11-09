// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"database/sql"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

var (
	_ evaluator.Compiler      = (*postgres.Schema)(nil)
	_ evaluator.FieldAccessor = (*postgres.Schema)(nil)
)

// TestSchemaEvaluatorLifecycle runs a Schema through filter/evaluator: validation, preparation, evaluation and lookups decode
// driver-owned values with sqlbase.Decoder, and a value decoder needs a compiling profile.
func TestSchemaEvaluatorLifecycle(t *testing.T) {
	t.Parallel()

	schemaFields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"n": sqlbase.Scalar("n", sqlbase.Numeric)})
	require.NoError(t, err)
	schema, err := postgres.NewSchema(schemaFields)
	require.NoError(t, err)
	eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAllowedFields("n")), evaluator.WithProfile(schema), evaluator.WithValueDecoder(sqlbase.Decoder{}))
	require.NoError(t, err)
	node := filtertest.MustParseFilter(t, `n == "1e100"`)
	require.NoError(t, eval.Validate(node))
	prepared, err := eval.Prepare(node)
	require.NoError(t, err)

	row := map[string]any{"n": sql.NullString{String: "1e100", Valid: true}}
	match, err := prepared.Evaluate(row)
	require.NoError(t, err)
	require.True(t, match)
	match, err = eval.Evaluate(node, row)
	require.NoError(t, err)
	require.True(t, match)

	value, present, err := eval.Lookup(row, "n")
	require.NoError(t, err)
	require.True(t, present)
	require.IsType(t, decimal.Decimal{}, value)

	_, _, err = eval.Lookup(row, "secret")
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
	require.ErrorIs(t, eval.Validate(filtertest.MustParseFilter(t, `true || secret == 1`)), filter.ErrFieldNotAllowed)
	prepared, err = eval.Prepare(nil)
	require.NoError(t, err)
	match, err = prepared.Evaluate(nil)
	require.NoError(t, err)
	require.True(t, match)

	_, err = evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true)), evaluator.WithValueDecoder(sqlbase.Decoder{}))
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
}
