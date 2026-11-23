// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/listing"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// defaultListCollation is the text collation the listing engine expects without sqlbase.WithListTextCollation: byte
// order, which matches the order of the page token values.
const defaultListCollation = "C"

const (
	listText    = "text"
	listVarchar = "varchar"
)

// listColumn is the catalog metadata of one table column.
type listColumn struct {
	dataType, udt, collation, nullable string
	// precision is the fractional-second precision of a timestamp or timestamp array.
	precision sql.NullInt64
}

// listSchemaChecker checks the catalog metadata of a listing table.
type listSchemaChecker struct {
	config    sqlbase.ListSchemaConfig
	collation string
}

func checkListSchema(ctx context.Context, db *sql.DB, c sqlbase.ListSchemaConfig) (err error) {
	if c.Schema != nil && len(c.Schema.CaseInsensitiveContainsFields()) > 0 {
		var supported bool
		if err = db.QueryRowContext(ctx, `SELECT pg_catalog.current_setting('server_version_num')::int / 10000 = 17
			AND pg_catalog.current_setting('server_encoding') = 'UTF8'
			AND pg_catalog.to_regcollation('pg_catalog.pg_c_utf8') IS NOT NULL`).Scan(&supported); err != nil {
			return err
		}

		if !supported {
			return coreerrs.Wrap(listing.ErrInvalidSchema, "case-insensitive contains requires PostgreSQL 17 with UTF8 encoding and pg_catalog.pg_c_utf8")
		}
	}

	rows, err := db.QueryContext(ctx, `SELECT c.column_name, c.data_type, c.udt_name, c.collation_name, c.is_nullable,
		CASE WHEN typ.typelem <> 0 THEN information_schema._pg_datetime_precision(typ.typelem, attr.atttypmod) ELSE c.datetime_precision END
		FROM information_schema.columns AS c
		JOIN pg_catalog.pg_class AS rel ON rel.relname = c.table_name
		JOIN pg_catalog.pg_namespace AS ns ON ns.oid = rel.relnamespace AND ns.nspname = c.table_schema
		JOIN pg_catalog.pg_attribute AS attr ON attr.attrelid = rel.oid AND attr.attname = c.column_name AND attr.attnum > 0
		JOIN pg_catalog.pg_type AS typ ON typ.oid = attr.atttypid
		WHERE c.table_schema = COALESCE(NULLIF($1, ''), current_schema()) AND c.table_name = $2`,
		sqlbase.Qualifier(c.Table), sqlbase.Unqualified(c.Table))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()

	got := map[string]listColumn{}

	for rows.Next() {
		var (
			name string
			col  listColumn
			coll sql.NullString
		)
		if err = rows.Scan(&name, &col.dataType, &col.udt, &coll, &col.nullable, &col.precision); err != nil {
			return err
		}

		col.collation = coll.String
		got[name] = col
	}

	if err = rows.Err(); err != nil {
		return err
	}

	checker := listSchemaChecker{config: c, collation: c.TextCollation}
	if checker.collation == "" {
		checker.collation = defaultListCollation
	}

	_, err = sqlbase.CheckListColumns(ctx, db, c, got, checker)

	return err
}

// CheckScopeColumn requires a NOT NULL scope column in the expected text collation.
func (s listSchemaChecker) CheckScopeColumn(ctx context.Context, db *sql.DB, column string, col listColumn) error {
	if err := s.config.Check(ctx, db, sqlbase.CheckNullability, column, col.nullable != "NO", "scope column %q must be NOT NULL",
		column); err != nil {
		return err
	}

	return s.config.Check(ctx, db, sqlbase.CheckCollation, column, (col.dataType == listText || col.udt == listVarchar) &&
		col.collation != s.collation, "scope column %q must use collation %q", column, s.collation)
}

// CheckColumn checks the type, nullability, collation and timestamp precision of a field or identity column.
func (s listSchemaChecker) CheckColumn(ctx context.Context, db *sql.DB, name string, f listing.Field, col listColumn, scoped bool) error {
	c := s.config
	kind := c.Kind(f.Kind)
	uuidIdentity := name == c.IDColumn && c.Storage.IDType == sqlbase.IDUUID

	typ := listSQLType(kind, f.Array)
	if uuidIdentity {
		typ = listSQLType(sqlbase.UUID, false)
	}

	typeOK := col.udt == typ
	if name == c.IDColumn && c.Storage.IDType == sqlbase.IDNative && c.Kind(c.IDKind) == sqlbase.Text &&
		(col.udt == listText || col.udt == listVarchar) {
		typeOK = true
	}

	if err := c.Check(ctx, db, sqlbase.CheckType, name, !typeOK, "column %q has type %q, expected %q", name, col.udt, typ); err != nil {
		return err
	}

	if err := c.Check(ctx, db, sqlbase.CheckNullability, name, !scoped && ((f.Presence && col.nullable != "YES") ||
		(!f.Presence && col.nullable != "NO")), "column %q nullability does not match presence declaration", name); err != nil {
		return err
	}

	if err := c.Check(ctx, db, sqlbase.CheckCollation, name, kind == sqlbase.Text && !uuidIdentity && col.collation != s.collation,
		"column %q must use collation %q", name, s.collation); err != nil {
		return err
	}
	// Stored timestamps must align with the configured cursor resolution. information_schema omits array precision;
	// the catalog query reads the element typmod for those columns.
	return c.Check(ctx, db, sqlbase.CheckTimestampPrecision, name, kind == sqlbase.Timestamp && (!col.precision.Valid ||
		!c.AlignsTimestamps(int(col.precision.Int64))), "column %q must align to timestamp precision %s", name, c.TimestampPrecision)
}

// listSQLType returns the PostgreSQL udt name of a storage kind, with the leading underscore of its array type.
func listSQLType(k sqlbase.Kind, array bool) string {
	m := map[sqlbase.Kind]string{sqlbase.Text: listText, sqlbase.UUID: "uuid", sqlbase.JSON: "jsonb",
		sqlbase.Integer: "int8", sqlbase.Boolean: "bool", sqlbase.Float: "float8", sqlbase.Bytes: "bytea",
		sqlbase.Timestamp: "timestamptz", sqlbase.Numeric: "numeric"}

	s := m[k]
	if array {
		return "_" + s
	}

	return s
}
