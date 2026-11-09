// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestCustomColumnGrammar(t *testing.T) {
	t.Parallel()

	for _, column := range []string{"tenant.record-id", "tenant.quote\"and`tick"} {
		t.Run(column, func(t *testing.T) {
			t.Parallel()

			fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar(column, sqlbase.Text)}, sqlbase.WithSchemaColumnValidator(func(string) error { return nil }))
			require.NoError(t, err)
			s, err := postgres.NewSchema(fields)
			require.NoError(t, err)
			p := compilePostgres(t, s, `name == "abc"`)
			sql, _, err := p.SQL()
			require.NoError(t, err)
			require.Contains(t, sql, `"`+strings.ReplaceAll(strings.Split(column, ".")[1], `"`, `""`)+`"`)

			contractSchema, err := mariadb.NewSchema(fields, mariadb.WithSchemaContractMode(true))
			require.NoError(t, err)
			tr, err := mariadb.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), contractSchema)
			require.NoError(t, err)
			sql, _, err = tr.Translate(filtertest.MustParseFilter(t, `name == "abc"`))
			require.NoError(t, err)
			require.Contains(t, sql, "`"+strings.ReplaceAll(strings.Split(column, ".")[1], "`", "``")+"`")
		})
	}
}

type customMatcher struct{}

func (customMatcher) MatchString(s string) bool { return s == "ab" }

func TestCustomRegexPolicy(t *testing.T) {
	t.Parallel()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar("name", sqlbase.Text)})
	require.NoError(t, err)

	compiles := 0
	opts := []filter.PolicyOption{filter.WithAnyField(true), filter.WithPortableContract(true),
		filter.WithContractRules(map[filter.ContractRule]bool{filter.PortableRegex: false}),
		filter.WithRegexCompiler(func(string) (filter.RegexMatcher, error) { compiles++; return customMatcher{}, nil }),
		filter.WithRegexValidator(func(string) error { return nil })}
	policy, err := filter.NewPolicy(opts...)
	require.NoError(t, err)
	node := filtertest.MustParseFilter(t, `name.matches("(?<=a)b$")`)
	s, err := postgres.NewSchema(fields, postgres.WithSchemaContractMode(true))
	require.NoError(t, err)
	p, err := s.Compile(policy, node)
	require.NoError(t, err)

	before := compiles

	for range 2 {
		got, err := p.Evaluate(map[string]any{"name": "ab"}, 1000, nil)
		require.NoError(t, err)
		require.True(t, got)
	}

	require.Equal(t, before, compiles, "evaluation must reuse the compiled matcher")

	_, _, err = p.SQLContract()
	require.NoError(t, err)
	contractSchema, err := mariadb.NewSchema(fields, mariadb.WithSchemaContractMode(true))
	require.NoError(t, err)
	tr, err := mariadb.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, opts...), contractSchema)
	require.NoError(t, err)
	_, _, err = tr.Translate(node)
	require.NoError(t, err)
}

// compilePostgres compiles expr against s under a policy admitting any field.
func compilePostgres(t testing.TB, s *postgres.Schema, expr string) *postgres.Program {
	t.Helper()

	policy, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)
	program, err := s.Compile(policy, filtertest.MustParseFilter(t, expr))
	require.NoError(t, err)

	return program
}
