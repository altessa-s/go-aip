// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"context"
	"database/sql"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

// nanosecondDigits is the number of fractional-second digits of a nanosecond.
const nanosecondDigits = 9

// Check reports the outcome of one built-in check through CheckPolicy: a failed check carries a
// [listing.ErrInvalidSchema] error described by format and args. Without a policy the error itself is returned, so a
// dialect returns the first failure.
func (c ListSchemaConfig) Check(ctx context.Context, db *sql.DB, check ListSchemaCheck, column string, failed bool,
	format string, args ...any,
) error {
	var err error
	if failed {
		err = invalid(format, args...)
	}

	if c.CheckPolicy != nil {
		return c.CheckPolicy(ctx, db, ListSchemaCheckResult{check, column, err})
	}

	return err
}

// Column returns the column of a declared field or scope key: its WithListColumns mapping, or the name itself.
func (c ListSchemaConfig) Column(name string) string {
	if col := c.Columns[name]; col != "" {
		return col
	}

	return name
}

// Kind returns the SQL storage class of a logical kind under KindResolver, or [DefaultSQLKind] without one.
func (c ListSchemaConfig) Kind(k filter.FieldKind) Kind {
	if c.KindResolver == nil {
		return DefaultSQLKind(k)
	}

	return c.KindResolver(k)
}

// FieldKind returns the SQL storage class of a declared field: UUID for one of UUIDFields, else [ListSchemaConfig.Kind] of
// its kind.
func (c ListSchemaConfig) FieldKind(f listing.Field) Kind {
	if f.Name != "" && slices.Contains(c.UUIDFields, f.Name) {
		return UUID
	}

	return c.Kind(f.Kind)
}

// KeyColumn reports whether column orders or pins rows: the identity, a scope column or the column of a field a
// request may sort on ([listing.Schema.SortFields]).
func (c ListSchemaConfig) KeyColumn(column string) bool {
	if column == c.IDColumn || slices.ContainsFunc(c.ScopeKeys, func(key string) bool { return c.Column(key) == column }) {
		return true
	}

	return slices.ContainsFunc(c.Schema.SortFields(), func(name string) bool { return c.Column(name) == column })
}

// AlignsTimestamps reports whether a column storing digits fractional-second digits is no finer than the configured
// TimestampPrecision, so every timestamp it stores is aligned to the cursor resolution.
func (c ListSchemaConfig) AlignsTimestamps(digits int) bool {
	return time.Duration(math.Pow10(nanosecondDigits-digits)) >= c.TimestampPrecision
}

// FitsDecimal reports whether a decimal column of precision and scale holds every value of the configured decimal
// format: no more fractional digits and no more integer digits.
func (c ListSchemaConfig) FitsDecimal(precision, scale int) bool {
	format := c.Storage.DecimalFormat
	return scale <= format.Scale && precision-scale <= format.Precision-format.Scale
}

// OrderKeys returns the physical cursor keys of a declared order, ending with the identity, or nil when the identity
// is not its last key.
func (c ListSchemaConfig) OrderKeys(o listing.Order) []ListOrderKey {
	return orderKeys(c.Schema, c.IDColumn, c.Kind(c.IDKind), c.IDKind, func(name string, k filter.FieldKind) Kind {
		return c.FieldKind(listing.Field{Name: name, Kind: k})
	}, c.Column, o)
}

// ListColumnChecker holds the database-specific checks of [CheckListColumns] over the metadata C of one column.
type ListColumnChecker[C any] interface {
	// CheckScopeColumn checks an existing scope column, which must be a NOT NULL scalar.
	CheckScopeColumn(ctx context.Context, db *sql.DB, column string, meta C) error
	// CheckColumn checks an existing field or identity column against its declaration. scoped reports a column that
	// is also a scope column, whose nullability the scope check owns.
	CheckColumn(ctx context.Context, db *sql.DB, column string, field listing.Field, meta C, scoped bool) error
}

// CheckListColumns runs the column checks every SQL dialect shares over got, the metadata of the table's columns by
// name: the projection columns, then the scope columns, then each declared field column and the identity column in
// column order, must exist ([CheckColumn]), and the existing scope and field columns go through checker. It returns
// the set of existing scope columns.
func CheckListColumns[C any](ctx context.Context, db *sql.DB, c ListSchemaConfig, got map[string]C,
	checker ListColumnChecker[C],
) (map[string]bool, error) {
	for _, projection := range c.Projection {
		if slices.Contains(c.DerivedColumns, projection) {
			continue
		}

		_, ok := got[projection]
		if err := c.Check(ctx, db, CheckColumn, projection, !ok, "missing projection column %q", projection); err != nil {
			return nil, err
		}
	}

	scope := make(map[string]bool, len(c.ScopeKeys))

	for _, key := range c.ScopeKeys {
		col := c.Column(key)

		meta, ok := got[col]
		if err := c.Check(ctx, db, CheckColumn, col, !ok, "missing scope column %q", col); err != nil {
			return nil, err
		}

		if !ok {
			continue
		}

		if err := checker.CheckScopeColumn(ctx, db, col, meta); err != nil {
			return nil, err
		}

		scope[col] = true
	}

	want := make(map[string]listing.Field)

	for _, name := range c.Schema.Fields() {
		f, _ := c.Schema.Field(name)
		want[c.Column(name)] = f
	}

	want[c.IDColumn] = listing.Field{Kind: c.IDKind}

	for _, name := range slices.Sorted(maps.Keys(want)) {
		if slices.Contains(c.DerivedColumns, name) {
			continue
		}

		meta, ok := got[name]
		if err := c.Check(ctx, db, CheckColumn, name, !ok, "missing column %q", name); err != nil {
			return nil, err
		}

		if !ok {
			continue
		}

		if err := checker.CheckColumn(ctx, db, name, want[name], meta, scope[name]); err != nil {
			return nil, err
		}
	}

	return scope, nil
}
