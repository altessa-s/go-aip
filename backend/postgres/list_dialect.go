// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// style is the PostgreSQL identifier quote and placeholder form: double quotes and $n placeholders.
var style = sqlbase.Style{Quote: '"', Numbered: true}

// ListDialect is the PostgreSQL dialect of the shared SQL listing engine ([sqlbase.NewListEngine]): PostgreSQL syntax
// (double-quoted identifiers, $n placeholders, row-value cursor comparisons), storage validation, schema checks over
// information_schema and pg_catalog, index management under advisory locks, and the PostgreSQL filter translator in
// contract mode. Embed it to override selected behavior. Its zero value is ready to use.
type ListDialect struct{}

// Quote quotes a validated identifier.
func (ListDialect) Quote(name string) string { return style.QuoteIdent(name) }

// Table validates and quotes a qualified table name.
func (ListDialect) Table(name string) (string, error) { return style.Table(name) }

// Bind renders positional placeholders.
func (ListDialect) Bind(query string, start int) string {
	return style.Bind(query, start)
}

// LimitSelect applies PostgreSQL's row limit.
func (ListDialect) LimitSelect(query string, limit int) string {
	return query + " LIMIT " + strconv.Itoa(limit)
}

// ValidIdentifier reports whether name is a supported PostgreSQL identifier.
func (ListDialect) ValidIdentifier(name string) bool { return sqlbase.IsPortableIdent(name) }

// IdentityExpression renders a scan-compatible identity expression.
func (ListDialect) IdentityExpression(column string, kind sqlbase.Kind) string {
	if kind != sqlbase.Text && kind != sqlbase.UUID {
		return column
	}

	return column + "::text"
}

// CursorPredicate renders a lexicographic continuation predicate: a row-value comparison when every key sorts in the
// same direction, nested comparisons otherwise. A UUID identity is cast to uuid.
func (ListDialect) CursorPredicate(keys []sqlbase.ListOrderKey, pos pagetoken.Position, alias string, id sqlbase.IDType,
	_ sqlbase.DecimalFormat,
) (string, []any, error) {
	vals, err := sqlbase.DecodeCursor(keys, pos, nil)
	if err != nil {
		return "", nil, err
	}

	last := len(keys) - 1
	cols := make([]string, len(keys))
	marks := make([]string, len(keys))
	sameDirection := true

	for i, k := range keys {
		cols[i] = style.QuoteIdent(alias) + "." + style.QuoteIdent(k.Column)
		marks[i] = "?"

		if i == last && id == sqlbase.IDUUID {
			marks[i] += "::uuid"
		}

		sameDirection = sameDirection && k.Desc == keys[0].Desc
	}

	if sameDirection {
		return "(" + strings.Join(cols, ", ") + ") " + cursorOperator(keys[0]) + " (" + strings.Join(marks, ", ") + ")", vals, nil
	}

	query := cols[last] + " " + cursorOperator(keys[last]) + " " + marks[last]
	args := []any{vals[last]}

	for i := last - 1; i >= 0; i-- {
		query = "(" + cols[i] + " " + cursorOperator(keys[i]) + " " + marks[i] + " OR (" + cols[i] + " = ? AND (" + query + ")))"
		args = append([]any{vals[i], vals[i]}, args...)
	}

	return "(" + query + ")", args, nil
}

// cursorOperator returns the comparison operator that continues after a key.
func cursorOperator(k sqlbase.ListOrderKey) string {
	if k.Desc {
		return "<"
	}

	return ">"
}

// Validate checks supported storage representations: PostgreSQL arrays or JSON arrays, and native or UUID identities.
func (ListDialect) Validate(_ *listing.Schema, config sqlbase.ListStorageConfig) error {
	if config.ArrayStorage != sqlbase.ArraysUnsupported && config.ArrayStorage != sqlbase.ArraysJSON {
		return coreerrs.Wrap(listing.ErrInvalidSchema, "unsupported array storage")
	}

	return nil
}

// IndexName returns a deterministic length-bounded name.
func (ListDialect) IndexName(table string, pattern int) string {
	return strings.Trim(style.IndexName(sqlbase.Unqualified(table), fmt.Sprintf("%02d_listing", pattern)), "\"`")
}

// IndexDDL renders CREATE INDEX.
func (ListDialect) IndexDDL(table, name string, columns []string) string {
	return "CREATE INDEX IF NOT EXISTS " + name + " ON " + table + " (" + strings.Join(columns, ", ") + ")"
}

// EnsureIndexes executes PostgreSQL DDL with the built-in advisory locking policy.
func (ListDialect) EnsureIndexes(ctx context.Context, db *sql.DB, table string, statements []string) error {
	return execDDL(ctx, db, []string{sqlbase.Unqualified(table)}, slices.Clone(statements))
}

// CheckSchema queries database metadata and applies configured checks.
func (ListDialect) CheckSchema(ctx context.Context, db *sql.DB, c sqlbase.ListSchemaConfig) error {
	return checkListSchema(ctx, db, c)
}

// NewTranslator builds the PostgreSQL listing filter translator.
func (ListDialect) NewTranslator(policy *filter.Policy, schema *sqlbase.Schema, opts []filter.TranslatorOption,
	config sqlbase.ListTranslatorConfig,
) (sqlbase.ListFilterTranslator, error) {
	return newListTranslator(policy, schema, opts, config)
}

// NewCaseInsensitiveContainsTranslator builds the listing translator with Unicode 15.1 simple lowercase contains on names.
func (ListDialect) NewCaseInsensitiveContainsTranslator(policy *filter.Policy, fields *sqlbase.Schema, opts []filter.TranslatorOption,
	config sqlbase.ListTranslatorConfig, names []string,
) (sqlbase.ListFilterTranslator, error) {
	return newListTranslator(policy, fields, opts, config, WithSchemaCaseInsensitiveContainsFields(names...))
}

func newListTranslator(policy *filter.Policy, fields *sqlbase.Schema, opts []filter.TranslatorOption,
	config sqlbase.ListTranslatorConfig, semanticOpts ...SchemaOption,
) (sqlbase.ListFilterTranslator, error) {
	semanticOpts = append(semanticOpts, WithSchemaContractMode(true), WithSchemaTimestampPrecision(config.TimestampPrecision))
	if config.TextCollation != "" {
		// This schema is used for SQL rendering only; comparison runs in the database.
		semanticOpts = append(semanticOpts, WithSchemaTextCollation(config.TextCollation), WithSchemaTextCompare(strings.Compare))
	}

	sem, err := NewSchema(fields, semanticOpts...)
	if err != nil {
		return nil, err
	}

	return NewFilterTranslatorWithSchema(policy, sem, WithFilterOptions(opts...))
}
