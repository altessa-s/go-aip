// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// style is the ClickHouse identifier quote and placeholder form: backticks and positional ? placeholders.
var style = sqlbase.Style{Quote: '`'}

// ListDialect is the ClickHouse dialect of the shared SQL listing engine ([sqlbase.NewListEngine]): ClickHouse syntax,
// storage validation, schema checks over system.columns, data skipping indexes, and the ClickHouse filter translator
// in contract mode. Embed it to override selected behavior. Its zero value is ready to use.
//
// Repeated fields are native Array(T) columns (the default array storage; ArraysJSON is rejected) and identities are
// native scalars (IDUUID is rejected: ClickHouse orders UUIDs by their two 64-bit halves, not by their text). Cursor
// operands are explicitly typed: decimals as Decimal(P, S) of the configured format, timestamps as DateTime64(9, 'UTC')
// from their exact text. Decimal identities and sort keys are selected with toString.
type ListDialect struct{}

// Quote quotes a validated identifier with backticks.
func (ListDialect) Quote(name string) string { return style.QuoteIdent(name) }

// Table validates and quotes a possibly database-qualified table name.
func (ListDialect) Table(name string) (string, error) { return style.Table(name) }

// Bind keeps ClickHouse's positional `?` placeholders.
func (ListDialect) Bind(query string, start int) string {
	return style.Bind(query, start)
}

// LimitSelect applies ClickHouse's row limit.
func (ListDialect) LimitSelect(query string, limit int) string {
	return query + " LIMIT " + strconv.Itoa(limit)
}

// ValidIdentifier reports whether name is a supported ClickHouse identifier.
func (ListDialect) ValidIdentifier(name string) bool { return sqlbase.IsPortableIdent(name) }

// IdentityExpression renders a scan-compatible identity expression: a decimal as its text.
func (ListDialect) IdentityExpression(column string, kind sqlbase.Kind) string {
	return listScanExpression(column, kind)
}

// SortKeyExpression renders a scan-compatible sort key expression: a decimal as its text. It implements
// [sqlbase.ListSortKeyDialect].
func (ListDialect) SortKeyExpression(column string, kind sqlbase.Kind) string {
	return listScanExpression(column, kind)
}

func listScanExpression(column string, kind sqlbase.Kind) string {
	if kind == sqlbase.Numeric {
		return "toString(" + column + ")"
	}

	return column
}

// CursorPredicate renders a lexicographic continuation predicate of nested comparisons with typed operands. Decimal
// operands must fit format.
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
		marks[i], vals[i] = listCursorOperand(k.StorageKind, vals[i], format)
	}

	query, args := sqlbase.ExpandKeysetPredicate(keys, cols, marks, vals)

	return query, args, nil
}

// listCursorOperand returns the typed placeholder of a cursor value and the argument it binds. Decimals and timestamps are
// bound as their exact text, so no driver formatting or server time zone can round or shift them.
func listCursorOperand(kind sqlbase.Kind, v any, format sqlbase.DecimalFormat) (string, any) {
	switch kind {
	case sqlbase.Numeric:
		if d, ok := v.(decimal.Decimal); ok {
			return fmt.Sprintf("CAST(? AS Decimal(%d, %d))", format.Precision, format.Scale), d.String()
		}
	case sqlbase.Timestamp:
		if t, ok := v.(time.Time); ok {
			return "toDateTime64(?, 9, 'UTC')", t.UTC().Format("2006-01-02 15:04:05.000000000")
		}
	default:
	}

	return "?", v
}

// Validate checks supported storage representations: native arrays and native identities.
func (ListDialect) Validate(_ *listing.Schema, config sqlbase.ListStorageConfig) error {
	if config.ArrayStorage != sqlbase.ArraysUnsupported {
		return coreerrs.Wrap(listing.ErrInvalidSchema, "ClickHouse stores repeated fields in native Array columns")
	}

	if config.IDType == sqlbase.IDUUID {
		return coreerrs.Wrap(listing.ErrInvalidSchema, "ClickHouse requires native ids to preserve listing order")
	}

	return nil
}

// IndexName returns a deterministic length-bounded name.
func (ListDialect) IndexName(table string, pattern int) string {
	return strings.Trim(style.IndexName(sqlbase.Unqualified(table), fmt.Sprintf("%02d_listing", pattern)), "\"`")
}

// IndexDDL renders a minmax data skipping index over the scope and sort columns of a pattern. A skipping index takes
// expressions, not directions, so the direction of each column fragment is dropped. The primary index of a MergeTree
// table is its sorting key, chosen when the table is created; see the package documentation.
func (ListDialect) IndexDDL(table, name string, columns []string) string {
	exprs := make([]string, len(columns))
	for i, column := range columns {
		column = strings.TrimSuffix(column, " ASC")
		exprs[i] = strings.TrimSuffix(column, " DESC")
	}

	return "ALTER TABLE " + table + " ADD INDEX IF NOT EXISTS " + name + " (" + strings.Join(exprs, ", ") + ") TYPE minmax GRANULARITY 1"
}

// EnsureIndexes executes the index DDL one statement at a time.
func (ListDialect) EnsureIndexes(ctx context.Context, db *sql.DB, _ string, statements []string) error {
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	return nil
}

// CheckSchema reads system.columns and applies the configured checks.
func (ListDialect) CheckSchema(ctx context.Context, db *sql.DB, c sqlbase.ListSchemaConfig) error {
	return checkListSchema(ctx, db, c)
}

// NewTranslator builds the ClickHouse listing filter translator. ClickHouse compares strings by their bytes and has no
// column collation, so a text collation (sqlbase.WithListTextCollation) fails with [filter.ErrInvalidOptions].
func (ListDialect) NewTranslator(policy *filter.Policy, schema *sqlbase.Schema, opts []filter.TranslatorOption,
	config sqlbase.ListTranslatorConfig,
) (sqlbase.ListFilterTranslator, error) {
	if config.TextCollation != "" {
		return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "ClickHouse compares strings by their bytes; a text collation is not supported")
	}

	sem, err := NewSchema(schema, WithSchemaContractMode(true), WithSchemaDecimalFormat(config.DecimalFormat),
		WithSchemaTimestampPrecision(config.TimestampPrecision))
	if err != nil {
		return nil, err
	}

	return NewFilterTranslatorWithSchema(policy, sem, WithFilterOptions(opts...))
}
