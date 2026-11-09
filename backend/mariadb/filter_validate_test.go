// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb_test

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestContractRejectsInvalidExpressions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		expr string
		want error
	}{
		{`name == 1`, filter.ErrFieldTypeMismatch},
		{`numbers == "x"`, filter.ErrFieldTypeMismatch},
		{`name && name`, filter.ErrFieldTypeMismatch},
		{`name`, filter.ErrInvalidExpression},
		{`name.size() == "x"`, filter.ErrFieldTypeMismatch},
		{`numbers.startsWith("x")`, filter.ErrFieldTypeMismatch},
		{`name.matches("(?i)é")`, filter.ErrUnsupportedOperation},
		{`name.matches("\\d")`, filter.ErrUnsupportedOperation},
		{`name == "ok" || numbers == "bad"`, filter.ErrFieldTypeMismatch},
		{`unknown == "x"`, filter.ErrInvalidOptions},
		{`name < null`, filter.ErrUnsupportedOperation},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			_, _, err := contractTranslator(t).Translate(filtertest.MustParseFilter(t, tc.expr))
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestContractLiteralLimits(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, field string
		value       any
		want        error
	}{
		{"integer overflow", "numbers", uint64(math.MaxUint64), filter.ErrFieldTypeMismatch},
		{"NUL", "name", "a\x00b", filter.ErrFieldTypeMismatch},
		{"UTF8", "name", "\xff", filter.ErrFieldTypeMismatch},
		{"budget", "name", strings.Repeat("a", 1<<22), filter.ErrDataLimitExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := contractTranslator(t).Translate(&filter.BinaryOpNode{Op: filter.OpEqual,
				Left: &filter.IdentNode{Name: tc.field}, Right: &filter.LiteralNode{Value: tc.value}})
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestContractMalformedCall(t *testing.T) {
	t.Parallel()
	_, _, err := contractTranslator(t).Translate(&filter.CallNode{Op: filter.OpContains, Target: &filter.IdentNode{Name: "name"}})
	require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)
}

func TestContractUnsupportedStorage(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		field      sqlbase.Field
		expression string
	}{
		{sqlbase.Scalar("value", sqlbase.JSON), `value == "{}"`},
		{sqlbase.Scalar("value", sqlbase.UUID), `value == "00000000-0000-0000-0000-000000000000"`},
		{sqlbase.JSONPath("doc", sqlbase.Text, "value"), `value == "text"`},
	} {
		schema, err := sqlbase.NewSchema(map[string]sqlbase.Field{"value": tc.field})
		require.NoError(t, err)
		contractSchema, err := mariadb.NewSchema(schema, mariadb.WithSchemaContractMode(true))
		require.NoError(t, err)
		translator, err := mariadb.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), contractSchema)
		require.NoError(t, err)
		_, _, err = translator.Translate(filtertest.MustParseFilter(t, tc.expression))
		require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
	}
}
