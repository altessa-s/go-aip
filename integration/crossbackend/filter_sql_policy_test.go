// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"

	maria "github.com/altessa-s/go-aip/backend/mariadb"
	pg "github.com/altessa-s/go-aip/backend/postgres"
	regexanchor "github.com/altessa-s/go-aip/filter/regexanchor"
)

func TestSQLSchemaPolicy(t *testing.T) {
	t.Parallel()

	rejected := errors.New("custom policy rejected literal")

	cases := []struct {
		name, expr string
		options    []filter.PolicyOption
		want       error
	}{
		{"operations", `name == "a"`, []filter.PolicyOption{filter.WithMaxOperations(1)}, filter.ErrMaxOperationsExceeded},
		{"depth", `!(name == "a")`, []filter.PolicyOption{filter.WithMaxDepth(1)}, filter.ErrMaxDepthExceeded},
		{"regex compiler", `name.matches("abc")`, []filter.PolicyOption{filter.WithRegexCompiler(func(string) (filter.RegexMatcher, error) { return nil, rejected })}, rejected},
		{"unsafe regex length", `name.matches("abc")`, []filter.PolicyOption{filter.WithUnsafeRegex(true), filter.WithMaxRegexLength(1)}, regexanchor.ErrInvalidRegex},
		{"array codec", `tags == "a"`, []filter.PolicyOption{filter.WithValueCodecs(map[filter.FieldKind]filter.ValueCodec{filter.FieldKindString: literalValidationCodec{err: rejected}})}, rejected},
		{"decimal parser", `price == 2`, []filter.PolicyOption{filter.WithDecimalParser(func(filter.Node, any) (decimal.Decimal, error) { return decimal.Decimal{}, rejected })}, rejected},
	}
	for _, tc := range cases {
		for _, backend := range []string{"postgres", "mariadb"} {
			t.Run(backend+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				schema, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar("name", sqlbase.Text), "tags": sqlbase.Array("tags", sqlbase.Text), "price": sqlbase.Scalar("price", sqlbase.Numeric)})
				require.NoError(t, err)

				opts := append([]filter.PolicyOption{filter.WithAnyField(true), filter.WithPortableContract(false), filter.WithFieldTypes(map[string]filter.FieldKind{"name": filter.FieldKindString, "tags": filter.FieldKindString, "price": filter.FieldKindDecimal}), filter.WithArrayFields("tags")}, tc.options...)
				node := filtertest.MustParseFilter(t, tc.expr)

				if backend == "postgres" {
					sem, err := pg.NewSchema(schema)
					require.NoError(t, err)
					tr, err := pg.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, opts...), sem)
					require.NoError(t, err)
					_, _, err = tr.Translate(node)
					require.ErrorIs(t, err, tc.want)
				} else {
					contractSchema, err := maria.NewSchema(schema, maria.WithSchemaContractMode(true))
					require.NoError(t, err)
					tr, err := maria.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, opts...), contractSchema)
					require.NoError(t, err)
					_, _, err = tr.Translate(node)
					require.ErrorIs(t, err, tc.want)
				}
			})
		}
	}
}

func TestSQLDecimalPolicyValue(t *testing.T) {
	t.Parallel()

	for _, expression := range []string{`price == "one"`, `price in ["one"]`, `price == "2"`, `price in ["2"]`} {
		t.Run(expression, func(t *testing.T) {
			t.Parallel()

			schema, err := sqlbase.NewSchema(map[string]sqlbase.Field{"price": sqlbase.Scalar("price", sqlbase.Numeric)})
			require.NoError(t, err)
			sem, err := pg.NewSchema(schema)
			require.NoError(t, err)

			opts := []filter.PolicyOption{filter.WithAnyField(true), filter.WithFieldTypes(map[string]filter.FieldKind{"price": filter.FieldKindDecimal}), filter.WithDecimalParser(func(n filter.Node, v any) (decimal.Decimal, error) {
				if v == "one" || v == "2" {
					return decimal.NewFromInt(1), nil
				}

				return filter.DecimalOperand(n, v)
			})}
			tr, err := pg.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, opts...), sem)
			require.NoError(t, err)
			_, args, err := tr.Translate(filtertest.MustParseFilter(t, expression))
			require.NoError(t, err)
			require.Equal(t, []any{"1"}, args)

			ev, err := evaluator.New(filtertest.MustPolicy(t, opts...), evaluator.WithProfile(sem))
			require.NoError(t, err)
			matched, err := ev.Evaluate(filtertest.MustParseFilter(t, expression), map[string]any{"price": decimal.NewFromInt(1)})
			require.NoError(t, err)
			require.True(t, matched)
		})
	}
}

// literalValidationCodec is a value codec whose literal validation returns err.
type literalValidationCodec struct{ err error }

func (c literalValidationCodec) ValidateLiteral(any) error { return c.err }
