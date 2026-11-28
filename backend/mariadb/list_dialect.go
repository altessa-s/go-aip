// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// style is the MariaDB identifier quote and placeholder form: backticks and positional ? placeholders.
var style = sqlbase.Style{Quote: '`'}

// ListDialect is the MariaDB dialect of the shared SQL listing engine ([sqlbase.NewListEngine]): MariaDB syntax
// (backtick-quoted identifiers, ? placeholders, nested cursor comparisons with decimals cast to the configured
// DECIMAL type), storage validation (text identities, JSON arrays), schema checks over information_schema, index
// management, and the MariaDB filter translator in contract mode. Embed it to override selected behavior. Its zero
// value is ready to use.
type ListDialect struct{}

// Quote quotes a validated identifier.
func (ListDialect) Quote(name string) string { return style.QuoteIdent(name) }

// Table validates and quotes a qualified table name.
func (ListDialect) Table(name string) (string, error) { return style.Table(name) }

// Bind renders positional placeholders.
func (ListDialect) Bind(query string, start int) string {
	return style.Bind(query, start)
}

// LimitSelect applies MariaDB's row limit.
func (ListDialect) LimitSelect(query string, limit int) string {
	return query + " LIMIT " + strconv.Itoa(limit)
}

// ValidIdentifier reports whether name is a supported MariaDB identifier.
func (ListDialect) ValidIdentifier(name string) bool { return sqlbase.IsPortableIdent(name) }

// IdentityExpression renders a scan-compatible identity expression.
func (ListDialect) IdentityExpression(column string, kind sqlbase.Kind) string {
	if kind != sqlbase.Text && kind != sqlbase.UUID {
		return column
	}

	return "CAST(" + column + " AS CHAR)"
}

// CursorPredicate renders a lexicographic continuation predicate of nested comparisons. Decimal operands are cast to
// the DECIMAL type of format and must fit it, so the server never rounds a cursor value.
func (ListDialect) CursorPredicate(keys []sqlbase.ListOrderKey, pos pagetoken.Position, alias string, _ sqlbase.IDType,
	format sqlbase.DecimalFormat,
) (string, []any, error) {
	vals, err := sqlbase.DecodeCursor(keys, pos, func(k sqlbase.ListOrderKey, v any) error {
		if k.StorageKind == sqlbase.Numeric {
			return format.Check(v)
		}

		return nil
	})
	if err != nil {
		return "", nil, err
	}

	cols := make([]string, len(keys))
	marks := make([]string, len(keys))

	for i, k := range keys {
		cols[i] = style.QuoteIdent(alias) + "." + style.QuoteIdent(k.Column)
		marks[i] = "?"

		if k.StorageKind == sqlbase.Numeric {
			typ, _ := format.SQL()
			marks[i] = "CAST(? AS " + typ + ")"
		}
	}

	query, args := sqlbase.ExpandKeysetPredicate(keys, cols, marks, vals)

	return query, args, nil
}

// Validate checks supported storage representations: JSON arrays and text identities. MariaDB has no UUID type that
// orders like its text, so IDUUID is rejected.
func (ListDialect) Validate(schema *listing.Schema, config sqlbase.ListStorageConfig) error {
	if config.ArrayStorage != sqlbase.ArraysUnsupported && config.ArrayStorage != sqlbase.ArraysJSON {
		return coreerrs.Wrap(listing.ErrInvalidSchema, "unsupported array storage")
	}

	if config.IDType == sqlbase.IDUUID {
		return coreerrs.Wrap(listing.ErrInvalidSchema, "MariaDB requires text ids to preserve listing order")
	}

	if config.HasUUIDFields {
		return coreerrs.Wrap(listing.ErrInvalidSchema, "MariaDB uuid fields need text columns: its UUID type does not order like the uuid text")
	}

	if config.NarrowIntegers || config.ExplicitCollation {
		return coreerrs.Wrap(listing.ErrInvalidSchema, "MariaDB requires 64-bit integers and collated text columns")
	}

	if config.ArrayStorage == sqlbase.ArraysUnsupported {
		for _, name := range schema.Fields() {
			if f, _ := schema.FieldInfo(name); f.Array {
				return coreerrs.Wrapf(listing.ErrInvalidSchema, "array field %q requires ArraysJSON", name)
			}
		}
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

// EnsureIndexes executes MariaDB index DDL.
func (ListDialect) EnsureIndexes(ctx context.Context, db *sql.DB, _ string, statements []string) error {
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	return nil
}

// CheckSchema queries database metadata and applies configured checks.
func (ListDialect) CheckSchema(ctx context.Context, db *sql.DB, c sqlbase.ListSchemaConfig) error {
	return checkListSchema(ctx, db, c)
}

// NewTranslator builds the MariaDB listing filter translator.
func (ListDialect) NewTranslator(policy *filter.Policy, schema *sqlbase.Schema, opts []filter.TranslatorOption,
	config sqlbase.ListTranslatorConfig,
) (sqlbase.ListFilterTranslator, error) {
	settings := []SchemaOption{WithSchemaContractMode(true), WithSchemaDecimalFormat(config.DecimalFormat),
		WithSchemaTimestampPrecision(config.TimestampPrecision)}
	if config.TextCollation != "" {
		settings = append(settings, WithSchemaTextCollation(config.TextCollation))
	}

	sem, err := NewSchema(schema, settings...)
	if err != nil {
		return nil, err
	}

	return NewFilterTranslatorWithSchema(policy, sem, WithFilterOptions(opts...))
}
