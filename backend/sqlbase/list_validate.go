// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"maps"
	"slices"
	"strings"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/listing"
)

func validateStorageOptions(cfg *listOptions, schema *listing.Schema) error {
	if cfg.kindResolver == nil {
		return invalid("nil SQL kind resolver")
	}

	if cfg.timestampPrecision <= 0 {
		return invalid("nonpositive timestamp precision")
	}

	switch cfg.kindResolver(cfg.idKind) {
	case Text, UUID, Integer, Boolean, Float, Numeric, Timestamp:
	default:
		return invalid("unsupported id kind")
	}

	if cfg.maxTime < 0 {
		return invalid("negative query timeout")
	}

	if cfg.parentKey != "" && !slices.Contains(cfg.scopeKeys, cfg.parentKey) {
		return invalid("parent key must be a declared scope key")
	}

	cfg.columns = maps.Clone(cfg.columns)
	if nilcheck.IsNil(cfg.dialect) {
		return invalid("nil dialect")
	}

	if !cfg.dialect.ValidIdentifier(cfg.tableAlias) {
		return invalid("invalid table alias")
	}

	if cfg.idType == IDUUID && cfg.kindResolver(cfg.idKind) != Text && cfg.kindResolver(cfg.idKind) != UUID {
		return invalid("UUID identity requires string kind")
	}

	if len(cfg.derivedColumns) > 0 && strings.TrimSpace(cfg.tableExpression) == "" {
		return invalid("derived columns require a table expression")
	}

	for _, column := range cfg.derivedColumns {
		if !cfg.dialect.ValidIdentifier(column) || column == cfg.idColumn {
			return invalid("derived column %q must be a valid identifier distinct from the identity column", column)
		}
	}

	for _, name := range cfg.uuidFields {
		f, ok := schema.FieldInfo(name)
		if !ok || f.Array || cfg.kindResolver(f.Kind) != Text {
			return invalid("UUID field %q must be a declared string scalar", name)
		}
	}

	if err := cfg.dialect.Validate(schema, cfg.storageConfig()); err != nil {
		return err
	}

	return nil
}

func (o *listOptions) storageConfig() ListStorageConfig {
	return ListStorageConfig{IDType: o.idType, ArrayStorage: o.arrayStorage, DecimalFormat: o.decimalFormat, HasUUIDFields: len(o.uuidFields) > 0,
		NarrowIntegers: o.narrowIntegers, ExplicitCollation: o.explicitCollation}
}

// validateNullableSorts refuses a declared order with a Presence key unless the dialect sorts nullable keys.
func validateNullableSorts(schema *listing.Schema, cfg *listOptions) error {
	if _, ok := cfg.dialect.(ListNullableSortDialect); ok {
		return nil
	}

	for _, order := range append(schema.Orders(), schema.Default()) {
		if name := nullableKey(schema, order); name != "" {
			return invalid("dialect does not sort on Presence field %q", name)
		}
	}

	return nil
}

// nullableKey returns the first Presence sort field of order, or "".
func nullableKey(schema *listing.Schema, order listing.Order) string {
	for _, key := range order.Keys() {
		if f, _ := schema.FieldInfo(key.Field); f.Presence {
			return key.Field
		}
	}

	return ""
}

func validateProjection[T any](cfg *listOptions, columns []string, dest func(*T) []any) error {
	if !cfg.dialect.ValidIdentifier(cfg.idColumn) || cfg.idColumn == "" {
		return invalid("id column %q", cfg.idColumn)
	}

	if cfg.idType != IDNative && cfg.idType != IDUUID {
		return invalid("unsupported id type")
	}

	destinations := dest(new(T))
	if len(destinations) != len(columns) {
		return invalid("destination count does not match columns")
	}

	if len(columns) == 0 {
		return invalid("at least one projection column is required")
	}

	for i, d := range destinations {
		if nilcheck.IsNil(d) {
			return invalid("destination %d is nil", i)
		}
	}

	for _, column := range columns {
		if !cfg.dialect.ValidIdentifier(column) {
			return invalid("projection column %q", column)
		}
	}

	return nil
}

func resolveFieldColumns(schema *listing.Schema, cfg *listOptions) (map[string]string, error) {
	fieldCols := map[string]string{}

	for _, name := range schema.Fields() {
		col := cfg.columns[name]
		if col == "" {
			col = name
		}

		if !cfg.dialect.ValidIdentifier(col) {
			return nil, invalid("field %q column %q", name, col)
		}

		fieldCols[name] = col
	}

	usedColumns := make(map[string]string, len(fieldCols))
	for _, name := range schema.Fields() {
		column := fieldCols[name]
		if previous, exists := usedColumns[column]; exists {
			return nil, invalid("fields %q and %q map to the same column %q", previous, name, column)
		}

		usedColumns[column] = name
		if column == cfg.idColumn {
			if cfg.idType != IDUUID && slices.Contains(cfg.uuidFields, name) {
				return nil, invalid("UUID field %q maps to the identity column and requires WithListIDType(IDUUID)", name)
			}

			field, _ := schema.FieldInfo(name)
			if field.Kind != cfg.idKind || field.Array || field.Optional || field.Presence {
				return nil, invalid("field %q mapped to id column must be a required scalar matching idKind", name)
			}
		}
	}

	for name, col := range cfg.columns {
		if _, ok := fieldCols[name]; !ok && !slices.Contains(cfg.scopeKeys, name) {
			return nil, invalid("column mapping for undeclared field %q", name)
		}

		if !cfg.dialect.ValidIdentifier(col) {
			return nil, invalid("field %q column %q", name, col)
		}
	}

	return fieldCols, nil
}

func validateScopeColumns(schema *listing.Schema, cfg *listOptions, fieldCols map[string]string) error {
	scopeUsed := make(map[string]string, len(cfg.scopeKeys))
	for _, key := range cfg.scopeKeys {
		if !cfg.dialect.ValidIdentifier(key) || key == cfg.idColumn {
			return invalid("scope key %q", key)
		}

		column := cfg.columns[key]
		if column == "" {
			column = key
		}

		if column == cfg.idColumn {
			return invalid("scope key %q maps to the identity column %q", key, column)
		}

		if previous, exists := scopeUsed[column]; exists {
			return invalid("scope keys %q and %q map to the same column %q", previous, key, column)
		}

		scopeUsed[column] = key

		field, ok := schema.FieldInfo(key)
		if ok && (field.Array || field.Presence) {
			return invalid("scope key %q must be a declared non-null scalar field", key)
		}
		// A scope pin is an equality on a canonical scalar; bytes have no canonical scope form.
		if ok && cfg.kindResolver(field.Kind) == Bytes {
			return invalid("scope key %q: a bytes field cannot pin a scope", key)
		}

		for _, fieldName := range schema.Fields() {
			field, _ := schema.FieldInfo(fieldName)
			if (field.Array || field.Presence) && fieldCols[fieldName] == column {
				return invalid("scope key %q shares column %q with array or presence field %q", key, column, fieldName)
			}
		}
	}

	return nil
}

func validateSortColumns(schema *listing.Schema, cfg *listOptions, fieldCols map[string]string) error {
	scopeColumns := make(map[string]bool, len(cfg.scopeKeys))
	for _, key := range cfg.scopeKeys {
		column := cfg.columns[key]
		if column == "" {
			column = key
		}

		scopeColumns[column] = true
	}

	for _, order := range append(schema.Orders(), schema.Default()) {
		seen := make(map[string]bool)

		keys := order.Keys()
		for i, key := range keys {
			if key.Nulls != 0 || key.Collation != "" {
				return invalid("null placement and collation are not supported")
			}

			column := fieldCols[key.Field]
			if column == cfg.idColumn && i != len(keys)-1 {
				return invalid("id column may only appear as the last sort key")
			}

			if seen[column] {
				return invalid("sort pattern contains duplicate column %q", column)
			}

			if scopeColumns[column] {
				return invalid("sort pattern contains scope column %q", column)
			}

			seen[column] = true
		}
	}

	return nil
}
