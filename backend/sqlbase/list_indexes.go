// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"context"
	"database/sql"
	"maps"
	"slices"
)

// Indexes returns CREATE INDEX statements for the schema's sort patterns.
func (l *ListEngine[T]) Indexes() []string { return slices.Clone(l.indexes) }

func (l *ListEngine[T]) compileIndexes() error {
	seen := map[string]bool{}

	orders := l.schema.Orders()
	for i := 0; i < len(orders); i += 2 {
		o := orders[i]

		ks := l.orderKeys(o)
		if ks == nil {
			return invalid("id may only appear as the last sort key")
		}

		parts := make([]string, 0, len(l.opts.scopeKeys)+len(ks))
		for _, scopeKey := range l.opts.scopeKeys {
			col := l.column(scopeKey)

			for _, k := range ks {
				if k.Column == col {
					return invalid("pattern %q sorts on scope key %q", o, scopeKey)
				}
			}

			parts = append(parts, l.quote(col)+" ASC")
		}

		for _, k := range ks {
			parts = append(parts, l.quote(k.Column)+" "+l.direction(k))
		}

		const directionsPerIndex = 2

		name := l.opts.dialect.IndexName(l.table, i/directionsPerIndex)
		if l.opts.indexNamer != nil {
			name = l.opts.indexNamer(l.table, i/directionsPerIndex)
		}

		if !l.opts.dialect.ValidIdentifier(name) {
			return invalid("invalid index name %q", name)
		}

		if seen[name] {
			return invalid("duplicate index name %q", name)
		}

		seen[name] = true
		l.indexes = append(l.indexes, l.opts.dialect.IndexDDL(l.quotedTable, l.quote(name), parts))
	}

	return nil
}

// EnsureIndexes validates the table storage contract and creates order indexes.
func (l *ListEngine[T]) EnsureIndexes(ctx context.Context, db *sql.DB) error {
	if err := l.CheckSchema(ctx, db); err != nil {
		return err
	}

	return l.opts.dialect.EnsureIndexes(ctx, db, l.table, slices.Clone(l.indexes))
}

// CheckSchema runs the dialect's metadata checks with the configured policy.
func (l *ListEngine[T]) CheckSchema(ctx context.Context, db *sql.DB) error {
	return l.opts.dialect.CheckSchema(ctx, db, ListSchemaConfig{
		Schema: l.schema, Table: l.table, Columns: maps.Collect(l.columnMap.All()),
		Projection: slices.Clone(l.columns), ScopeKeys: slices.Clone(l.opts.scopeKeys),
		IDColumn: l.opts.idColumn, IDKind: l.opts.idKind,
		Storage:            l.opts.storageConfig(),
		TimestampPrecision: l.opts.timestampPrecision, TextCollation: l.opts.textCollation,
		CheckPolicy: l.opts.schemaCheckPolicy, KindResolver: l.opts.kindResolver, UUIDFields: slices.Clone(l.opts.uuidFields),
		DerivedColumns: slices.Clone(l.opts.derivedColumns),
	})
}
