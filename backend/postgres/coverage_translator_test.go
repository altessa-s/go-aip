// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/metrics"
)

// TestCoverageScalarTranslator renders array, decimal and escaped string predicates through the scalar translator, and
// propagates a literal encoder failure from every value-binding template.
func TestCoverageScalarTranslator(t *testing.T) {
	t.Parallel()

	policy := filtertest.MustPolicy(t, filter.WithAllowedFields("tags", "price", "name"), filter.WithArrayFields("tags"),
		filter.WithFieldTypes(map[string]filter.FieldKind{"tags": filter.FieldKindString, "price": filter.FieldKindDecimal, "name": filter.FieldKindString}))

	tr, err := postgres.NewFilterTranslator(policy, postgres.WithFilterCollector(metrics.Noop()), postgres.WithFilterCollector(nil))
	require.NoError(t, err)

	for _, tc := range []struct {
		expr string
		want string
	}{
		{`tags == "a"`, `$1 = ANY("tags")`},
		{`tags.contains("a")`, `EXISTS (SELECT 1 FROM unnest("tags") AS _aip_e(v) WHERE strpos(_aip_e.v, $1) > 0)`},
		{`price == 1.25`, `CAST($1 AS NUMERIC)`},
	} {
		sql, _, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
		require.NoError(t, err, tc.expr)
		require.Contains(t, sql, tc.want, tc.expr)
		require.NoError(t, tr.ValidateAST(filtertest.MustParseFilter(t, tc.expr)))
	}

	inline, err := tr.TranslateInline(filtertest.MustParseFilter(t, `name == "a\r\tb"`))
	require.NoError(t, err)
	require.Contains(t, inline, `E'a\r\tb'`)
	require.Error(t, tr.ValidateAST(filtertest.MustParseFilter(t, `secret == 1`)))

	errEncode := errors.New("encode")
	failing, err := postgres.NewFilterTranslator(policy, postgres.WithFilterLiteralEncoder(func(any) (any, error) { return nil, errEncode }))
	require.NoError(t, err)

	for _, expr := range []string{`tags == "a"`, `tags.contains("a")`, `price == 1.25`} {
		_, _, err = failing.Translate(filtertest.MustParseFilter(t, expr))
		require.ErrorIs(t, err, errEncode, expr)
	}

	_, err = postgres.NewFilterTranslator(nil)
	require.ErrorIs(t, err, filter.ErrPolicyRequired)
}

// TestCoverageSchemaTranslator validates the schema translator's construction and renders both modes.
func TestCoverageSchemaTranslator(t *testing.T) {
	t.Parallel()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar("name", sqlbase.Text)})
	require.NoError(t, err)
	schema, err := postgres.NewSchema(fields)
	require.NoError(t, err)
	contract, err := postgres.NewSchema(fields, postgres.WithSchemaContractMode(true))
	require.NoError(t, err)

	policy := filtertest.MustPolicy(t, filter.WithAllowedFields("name"))

	for _, tc := range []struct {
		name    string
		policy  *filter.Policy
		schema  *postgres.Schema
		opts    []postgres.FilterOption
		wantErr error
	}{
		{"nil schema", policy, nil, nil, filter.ErrInvalidOptions},
		{"literal encoder", policy, schema, []postgres.FilterOption{postgres.WithFilterLiteralEncoder(func(v any) (any, error) { return v, nil })}, filter.ErrInvalidOptions},
		{"predicate renderer", policy, schema, []postgres.FilterOption{postgres.WithFilterPredicateRenderer(func(filter.Node, sqlbase.ValueFunc) (string, bool, error) { return "", false, nil })}, filter.ErrInvalidOptions},
		{"nil policy", nil, schema, nil, filter.ErrPolicyRequired},
		{"conflicting type", filtertest.MustPolicy(t, filter.WithAllowedFields("name"), filter.WithFieldTypes(map[string]filter.FieldKind{"name": filter.FieldKindInt})), schema, nil, filter.ErrInvalidOptions},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := postgres.NewFilterTranslatorWithSchema(tc.policy, tc.schema, tc.opts...)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}

	for _, s := range []*postgres.Schema{schema, contract} {
		tr, err := postgres.NewFilterTranslatorWithSchema(policy, s, postgres.WithFilterCollector(metrics.Noop()))
		require.NoError(t, err)

		sql, args, err := tr.Translate(filtertest.MustParseFilter(t, `name == "a"`))
		require.NoError(t, err)
		require.Equal(t, []any{"a"}, args)

		inline, err := tr.TranslateInline(filtertest.MustParseFilter(t, `name == "a"`))
		require.NoError(t, err)

		if s.ContractMode() {
			require.NotContains(t, sql, "COLLATE")
			require.NotContains(t, inline, "COLLATE")
		} else {
			require.Contains(t, sql, "COLLATE")
			require.Contains(t, inline, "COLLATE")
		}

		bad := filtertest.MustParseFilter(t, `name == 1`)
		_, _, err = tr.Translate(bad)
		require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)
		_, err = tr.TranslateInline(bad)
		require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)
		require.ErrorIs(t, tr.ValidateAST(bad), filter.ErrFieldTypeMismatch)
	}
}

// TestCoverageListDialectTranslator builds the listing translator with and without an explicit collation and rejects invalid
// semantic options.
func TestCoverageListDialectTranslator(t *testing.T) {
	t.Parallel()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar("name", sqlbase.Text)})
	require.NoError(t, err)

	policy := filtertest.MustPolicy(t, filter.WithAllowedFields("name"))

	for _, tc := range []struct {
		name    string
		config  sqlbase.ListTranslatorConfig
		wantErr bool
	}{
		{"default", sqlbase.ListTranslatorConfig{TimestampPrecision: time.Microsecond}, false},
		{"collation", sqlbase.ListTranslatorConfig{TextCollation: "en_US", TimestampPrecision: time.Microsecond}, false},
		{"bad collation", sqlbase.ListTranslatorConfig{TextCollation: "a\x00"}, true},
		{"negative precision", sqlbase.ListTranslatorConfig{TimestampPrecision: -time.Second}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tr, err := postgres.ListDialect{}.NewTranslator(policy, fields, nil, tc.config)
			if tc.wantErr {
				require.ErrorIs(t, err, filter.ErrInvalidOptions)
				return
			}

			require.NoError(t, err)

			sql, _, err := tr.Translate(filtertest.MustParseFilter(t, `name == "a"`))
			require.NoError(t, err)
			require.Equal(t, `("name" = ($1)::text)`, sql)
		})
	}
}
