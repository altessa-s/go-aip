// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"context"
	"database/sql"
	"time"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

// ListDialect supplies SQL syntax, filter translation, schema checks, and index management.
// Implementations and their translators must support concurrent calls. SQL fragments
// are trusted configuration, never request input. The PostgreSQL, MariaDB and ClickHouse dialects are
// postgres.ListDialect, mariadb.ListDialect and clickhouse.ListDialect of the backend packages; embed one of them to
// override only selected behavior, or implement the component interfaces for another database with the shared
// building blocks [DecodeCursor], [ExpandKeysetPredicate] and [CheckListColumns].
type ListDialect interface {
	ListSyntax
	ListFilterDialect
	ListSchemaDialect
	ListIndexDialect
}

// ListSyntax renders identifiers and cursor expressions. CursorPredicate returns
// question-mark placeholders; Bind replaces unquoted question marks starting at start.
// Filter SQL is already bound by its translator and never passed to Bind.
// ValidIdentifier validates unquoted names before Quote; Table validates its input.
type ListSyntax interface {
	ValidIdentifier(string) bool
	Quote(string) string
	Table(string) (string, error)
	Bind(query string, start int) string
	// LimitSelect applies the dialect-specific row limit to a SELECT without a limit.
	LimitSelect(query string, limit int) string
	IdentityExpression(quotedColumn string, kind Kind) string
	CursorPredicate([]ListOrderKey, pagetoken.Position, string, IDType, DecimalFormat) (string, []any, error)
}

// ListFilterTranslator translates a validated filter to SQL with native bound placeholders
// starting at parameter 1 and ordered arguments. The engine preserves this SQL verbatim.
type ListFilterTranslator interface {
	Translate(filter.Node) (string, []any, error)
}

// ListFilterDialect validates storage choices and builds a concurrent filter translator.
type ListFilterDialect interface {
	Validate(*listing.Schema, ListStorageConfig) error
	NewTranslator(*filter.Policy, *Schema, []filter.TranslatorOption, ListTranslatorConfig) (ListFilterTranslator, error)
}

// ListSchemaDialect owns metadata queries and storage validation.
type ListSchemaDialect interface {
	CheckSchema(context.Context, *sql.DB, ListSchemaConfig) error
}

// ListIndexDialect names, renders, and executes index DDL. Names are unquoted identifiers;
// IndexDDL receives a quoted table, quoted name, and quoted column/direction fragments.
type ListIndexDialect interface {
	IndexName(table string, pattern int) string
	IndexDDL(table, name string, columns []string) string
	EnsureIndexes(context.Context, *sql.DB, string, []string) error
}

// ListStorageConfig describes representation choices passed to a dialect.
type ListStorageConfig struct {
	IDType        IDType
	ArrayStorage  ArrayStorage
	DecimalFormat DecimalFormat
	// UUIDFields are the string fields stored in uuid columns ([WithListUUIDFields]).
	UUIDFields []string
	// NarrowIntegers accepts narrower signed integer columns ([WithListNarrowIntegers]).
	NarrowIntegers bool
	// ExplicitCollation makes filters collate text explicitly ([WithListExplicitCollation]).
	ExplicitCollation bool
}

// ListSchemaConfig is an isolated snapshot of the engine's storage expectations.
// Schema is immutable. Dialects must treat the remaining data as read-only.
type ListSchemaConfig struct {
	Schema                *listing.Schema
	Table                 string
	Columns               map[string]string
	Projection, ScopeKeys []string
	IDColumn              string
	IDKind                filter.FieldKind
	Storage               ListStorageConfig
	TimestampPrecision    time.Duration
	TextCollation         string
	CheckPolicy           ListSchemaCheckPolicy
	// KindResolver maps logical kinds to their SQL storage representation.
	KindResolver func(filter.FieldKind) Kind
	// UUIDFields are the string fields stored in uuid columns.
	UUIDFields []string
	// DerivedColumns are the columns only the table expression has; checks skip them.
	DerivedColumns []string
}

// IndexNamer returns an unquoted index name for a table and zero-based sort pattern.
// It must be deterministic; duplicate or invalid names fail construction.
type IndexNamer func(table string, pattern int) string

// ListSchemaCheck identifies an independently replaceable storage check.
type ListSchemaCheck string

const (
	// CheckColumn requires configured columns to exist.
	CheckColumn ListSchemaCheck = "column"
	// CheckType compares physical and declared field types.
	CheckType ListSchemaCheck = "type"
	// CheckNullability checks required, scope, and Presence storage.
	CheckNullability ListSchemaCheck = "nullability"
	// CheckCollation checks text ordering and charset expectations.
	CheckCollation ListSchemaCheck = "collation"
	// CheckTimestampPrecision checks declared timestamp resolution.
	CheckTimestampPrecision ListSchemaCheck = "timestamp_precision"
	// CheckDecimalDomain checks that a MariaDB or ClickHouse decimal column fits the configured DecimalFormat.
	CheckDecimalDomain ListSchemaCheck = "decimal_domain"
	// CheckSignedInteger requires signed MariaDB integers.
	CheckSignedInteger ListSchemaCheck = "signed_integer"
	// CheckIndexable requires fully indexable MariaDB text types.
	CheckIndexable ListSchemaCheck = "indexable"
)

// ListSchemaCheckResult describes a built-in check. Err is nil when the default check
// passes. Policies run for passing and failing checks and may query db to replace
// the check. Returning nil disables a check; returning result.Err preserves it.
// Missing columns cannot have subsequent type or value checks.
type ListSchemaCheckResult struct {
	Check  ListSchemaCheck
	Column string
	Err    error
}

// ListSchemaCheckPolicy replaces a check result. It must be safe for concurrent use.
type ListSchemaCheckPolicy func(context.Context, *sql.DB, ListSchemaCheckResult) error

// ListSortKeyDialect is an optional extension of a [ListDialect] that renders the SELECT expression of a non-identity
// sort key, as [ListSyntax.IdentityExpression] does for the identity. The engine scans the expression into the
// destination of the key's storage kind (a string for Numeric). Without it the quoted column is selected.
type ListSortKeyDialect interface {
	SortKeyExpression(quotedColumn string, kind Kind) string
}

// ListNullableSortDialect is an optional extension of a [ListDialect] that sorts on Presence fields. NULL orders
// before every value: first ascending and last descending. [ListSyntax.CursorPredicate] then receives a nil value for
// a NULL key, as [DecodeCursor] decodes it and [ExpandKeysetPredicate] renders it. Without it the engine refuses a
// Presence sort key.
type ListNullableSortDialect interface {
	// NullableSortDirection renders the ORDER BY and index direction of a nullable key, such as "ASC NULLS FIRST".
	NullableSortDirection(desc bool) string
}

// sortKeySelect renders the SELECT expression of a non-identity sort key.
func (l *ListEngine[T]) sortKeySelect(k ListOrderKey) string {
	col := l.quote(k.Column)
	if d, ok := l.opts.dialect.(ListSortKeyDialect); ok {
		return d.SortKeyExpression(col, k.StorageKind)
	}

	return col
}

// ListTranslatorConfig carries storage precision to filter translation.
type ListTranslatorConfig struct {
	TextCollation      string
	DecimalFormat      DecimalFormat
	TimestampPrecision time.Duration
	// ExplicitCollation compares text under TextCollation (the dialect's default when empty) whatever the column's.
	ExplicitCollation bool
}
