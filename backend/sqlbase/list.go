// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"

	coreMaps "github.com/altessa-s/go-atlas-core/collections/maps"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Querier runs the parameterized SELECT used by a list.
type Querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Scope pins each key declared by [WithListScopeKeys] for every page in the list.
type Scope map[string]any

// IDType selects native scalar storage or PostgreSQL UUID storage for the identity.
type IDType uint8

const (
	// IDNative uses native scalar storage selected by WithListIDKind (string by default).
	IDNative IDType = iota
	// IDUUID stores canonical UUID ids in a uuid column.
	IDUUID
)

// ListEngine serves one SQL keyset page at a time. Build a ListEngine once per
// table and share it between concurrent requests.
type ListEngine[T any] struct {
	q                  Querier
	codec              pagetoken.Signer
	schema             *listing.Schema
	table, quotedTable string
	columns            []string
	dest               func(*T) []any
	opts               *listOptions
	translator         ListFilterTranslator
	indexes            []string
	columnMap          *coreMaps.ImmutableMap[string, string]
	tokens             pagetoken.Typed[pagetoken.Position]
	// plans holds the query plans of the declared orders by canonical order; it is read-only after construction.
	plans map[string]*listPlan
}

// NewListEngine constructs a keyset list over the SQL database of dialect. dest returns Scan destinations in
// the same order as columns. A nil codec serves a single page only. A nil dialect fails with
// [listing.ErrInvalidSchema]; the PostgreSQL, MariaDB and ClickHouse dialects are postgres.ListDialect,
// mariadb.ListDialect and clickhouse.ListDialect, whose NewListEngine constructors fix the dialect.
func NewListEngine[T any](q Querier, codec pagetoken.Signer, schema *listing.Schema, table string, columns []string, dest func(*T) []any,
	dialect ListDialect, opts ...ListEngineOption) (*ListEngine[T], error) {
	if schema == nil || nilcheck.IsNil(q) || dest == nil {
		return nil, invalid("nil schema, querier, or destination")
	}

	cfg := newListOptions(opts...)
	cfg.dialect = dialect

	if err := validateStorageOptions(cfg, schema); err != nil {
		return nil, err
	}

	quotedTable, err := cfg.dialect.Table(table)
	if err != nil {
		return nil, coreerrs.Wrap(errors.Join(listing.ErrInvalidSchema, err), "table")
	}

	if err = validateProjection(cfg, columns, dest); err != nil {
		return nil, err
	}

	l := &ListEngine[T]{q: q, codec: codec, schema: schema, table: table, quotedTable: quotedTable, columns: slices.Clone(columns), dest: dest, opts: cfg}

	fieldCols, err := resolveFieldColumns(schema, cfg)
	if err != nil {
		return nil, err
	}

	if err = validateScopeColumns(schema, cfg, fieldCols); err != nil {
		return nil, err
	}

	if err = validateSortColumns(schema, cfg, fieldCols); err != nil {
		return nil, err
	}

	sqlFields := make(map[string]Field, len(fieldCols))

	for _, name := range schema.Fields() {
		f, _ := schema.FieldInfo(name)

		k := cfg.kindResolver(f.Kind)
		switch {
		case fieldCols[name] == cfg.idColumn && cfg.idType == IDUUID:
			sqlFields[name] = Scalar(fieldCols[name], UUID)
		case f.Array:
			sqlFields[name] = Array(fieldCols[name], k)
		case f.Presence:
			sqlFields[name] = PresenceScalar(fieldCols[name], k)
		default:
			sqlFields[name] = Scalar(fieldCols[name], k)
		}
	}

	sem, err := NewSchema(sqlFields, WithSchemaKindValidator(func(logical filter.FieldKind, storage Kind) bool {
		resolved := cfg.kindResolver(logical)
		return resolved == storage || (storage == UUID && resolved == Text)
	}), WithSchemaColumnValidator(func(column string) error {
		if !cfg.dialect.ValidIdentifier(column) {
			return invalid("invalid column %q", column)
		}

		return nil
	}))
	if err != nil {
		return nil, invalid("filter schema: %w", err)
	}

	// The schema's policy authorizes the public names; the translator binds them to the columns.
	binding := []filter.TranslatorOption{filter.WithFieldMapping(fieldCols)}

	l.translator, err = cfg.dialect.NewTranslator(schema.FilterPolicy(), sem, binding, ListTranslatorConfig{TextCollation: cfg.textCollation,
		DecimalFormat: cfg.decimalFormat, TimestampPrecision: cfg.timestampPrecision})
	if err != nil {
		return nil, coreerrs.Wrap(errors.Join(listing.ErrInvalidSchema, err), "filter translator")
	}

	l.columnMap = coreMaps.NewImmutableMap(cfg.columns)
	cfg.columns = nil

	if nilcheck.IsNil(l.translator) {
		return nil, invalid("nil filter translator")
	}

	if err = l.compileIndexes(); err != nil {
		return nil, err
	}

	l.tokens = pagetoken.NewTyped[pagetoken.Position](codec, pagetoken.WithPayloadCodec(cfg.positionCodec))
	l.compilePlans()

	return l, nil
}

// MustNewListEngine is [NewListEngine] for package-level declarations with static configuration: it returns the engine
// NewListEngine builds and panics on an invalid configuration. The panic value is an error prefixed with
// "sqlbase.MustNewListEngine: " that wraps the NewListEngine error, so errors.Is(recovered, [listing.ErrInvalidSchema])
// holds. Use NewListEngine where the configuration comes from input rather than code.
func MustNewListEngine[T any](q Querier, codec pagetoken.Signer, schema *listing.Schema, table string, columns []string, dest func(*T) []any,
	dialect ListDialect, opts ...ListEngineOption) *ListEngine[T] {
	l, err := NewListEngine(q, codec, schema, table, columns, dest, dialect, opts...)
	if err != nil {
		panic(fmt.Errorf("sqlbase.MustNewListEngine: %w", err))
	}

	return l
}

func invalid(format string, args ...any) error {
	return coreerrs.Wrapf(listing.ErrInvalidSchema, format, args...)
}

// DefaultSQLKind returns the built-in SQL storage class, or Unknown for a custom kind.
// A custom WithListKindResolver can delegate its remaining kinds to this function.
func DefaultSQLKind(k filter.FieldKind) Kind {
	switch k {
	case filter.FieldKindString:
		return Text
	case filter.FieldKindInt:
		return Integer
	case filter.FieldKindBool:
		return Boolean
	case filter.FieldKindFloat:
		return Float
	case filter.FieldKindBytes:
		return Bytes
	case filter.FieldKindTimestamp:
		return Timestamp
	case filter.FieldKindDecimal:
		return Numeric
	default:
		return Unknown
	}
}

func (l *ListEngine[T]) quote(s string) string { return l.opts.dialect.Quote(s) }

// column returns the column of a declared field or scope key.
func (l *ListEngine[T]) column(name string) string {
	if col, _ := l.columnMap.Get(name); col != "" {
		return col
	}

	return name
}
