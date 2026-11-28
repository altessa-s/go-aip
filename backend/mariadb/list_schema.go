// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/listing"
)

// defaultListCollation is the text collation the listing engine expects without sqlbase.WithListTextCollation: binary
// order without trailing-space padding, which matches the order of the page token values.
const defaultListCollation = "utf8mb4_nopad_bin"

const (
	listText     = "text"
	listLongText = "longtext"
	listChar     = "char"
	listVarchar  = "varchar"
)

// listColumn is the information_schema metadata of one table column.
type listColumn struct {
	dataType, columnType, charset, collation, nullable string
	precision                                          sql.NullInt64
}

// listSchemaChecker checks the information_schema metadata of a listing table.
type listSchemaChecker struct {
	config    sqlbase.ListSchemaConfig
	collation string
}

func checkListSchema(ctx context.Context, db *sql.DB, c sqlbase.ListSchemaConfig) (err error) {
	rows, err := db.QueryContext(ctx, `SELECT COLUMN_NAME, DATA_TYPE, COLUMN_TYPE, CHARACTER_SET_NAME, COLLATION_NAME,
		IS_NULLABLE, DATETIME_PRECISION FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = COALESCE(NULLIF(?, ''), DATABASE()) AND TABLE_NAME = ?`,
		sqlbase.Qualifier(c.Table), sqlbase.Unqualified(c.Table))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()

	got := make(map[string]listColumn)

	for rows.Next() {
		var (
			name               string
			col                listColumn
			charset, collation sql.NullString
		)
		if err = rows.Scan(&name, &col.dataType, &col.columnType, &charset, &collation, &col.nullable, &col.precision); err != nil {
			return err
		}

		col.charset, col.collation = charset.String, collation.String
		got[name] = col
	}

	if err = rows.Err(); err != nil {
		return err
	}

	checker := listSchemaChecker{config: c, collation: c.TextCollation}
	if checker.collation == "" {
		checker.collation = defaultListCollation
	}

	scope, err := sqlbase.CheckListColumns(ctx, db, c, got, checker)
	if err != nil {
		return err
	}

	// Index keys must be fully indexable: MariaDB indexes only a prefix of a TEXT column.
	indexed := maps.Clone(scope)

	for _, order := range append(c.Schema.Orders(), c.Schema.Default()) {
		for _, key := range c.OrderKeys(order) {
			indexed[key.Column] = true
		}
	}

	for _, name := range slices.Sorted(maps.Keys(indexed)) {
		if slices.Contains(c.DerivedColumns, name) {
			continue
		}

		col := got[name]
		if err := c.Check(ctx, db, sqlbase.CheckIndexable, name, listTextType(col.dataType) && col.dataType != listVarchar && col.dataType != listChar,
			"indexed column %q must use VARCHAR or CHAR, not TEXT", name); err != nil {
			return err
		}
	}

	return nil
}

// CheckScopeColumn requires a NOT NULL scope column in utf8mb4 and the expected collation.
func (s listSchemaChecker) CheckScopeColumn(ctx context.Context, db *sql.DB, column string, col listColumn) error {
	if err := s.config.Check(ctx, db, sqlbase.CheckNullability, column, col.nullable != "NO", "scope column %q must be NOT NULL",
		column); err != nil {
		return err
	}

	return s.config.Check(ctx, db, sqlbase.CheckCollation, column, listTextType(col.dataType) && (col.charset != "utf8mb4" ||
		col.collation != s.collation), "scope column %q must use utf8mb4 and collation %q", column, s.collation)
}

// CheckColumn checks the type, decimal domain, signedness, nullability, collation and timestamp precision of a field
// or identity column.
func (s listSchemaChecker) CheckColumn(ctx context.Context, db *sql.DB, name string, f listing.Field, col listColumn, scoped bool) error {
	c := s.config
	kind := c.FieldKind(f)

	if f.Array && c.Storage.ArrayStorage == sqlbase.ArraysJSON {
		if err := c.Check(ctx, db, sqlbase.CheckType, name, col.dataType != listLongText && col.dataType != "json",
			"array column %q must use JSON", name); err != nil {
			return err
		}
	} else if err := c.Check(ctx, db, sqlbase.CheckType, name, !listTypeMatches(col.dataType, kind, name == c.IDColumn),
		"column %q has type %q, expected %q", name, col.dataType, listTypeName(kind)); err != nil {
		return err
	}

	if !f.Array && kind == sqlbase.Numeric {
		var precision, scale int

		_, parseErr := fmt.Sscanf(strings.ToLower(col.columnType), "decimal(%d,%d)", &precision, &scale)
		if err := c.Check(ctx, db, sqlbase.CheckDecimalDomain, name, parseErr != nil || !c.FitsDecimal(precision, scale),
			"column %q exceeds configured decimal domain", name); err != nil {
			return err
		}
	}

	if err := c.Check(ctx, db, sqlbase.CheckSignedInteger, name, !f.Array && kind == sqlbase.Integer &&
		strings.Contains(strings.ToLower(col.columnType), "unsigned"), "column %q must use signed BIGINT", name); err != nil {
		return err
	}

	if err := c.Check(ctx, db, sqlbase.CheckNullability, name, !scoped && ((f.Presence && col.nullable != "YES") ||
		(!f.Presence && col.nullable != "NO")), "column %q nullability does not match presence declaration", name); err != nil {
		return err
	}

	if err := c.Check(ctx, db, sqlbase.CheckCollation, name, (listTextType(col.dataType) || f.Array) && (col.charset != "utf8mb4" ||
		col.collation != s.collation), "column %q must use utf8mb4 and collation %q", name, s.collation); err != nil {
		return err
	}

	return c.Check(ctx, db, sqlbase.CheckTimestampPrecision, name, !f.Array && kind == sqlbase.Timestamp &&
		(!col.precision.Valid || !c.AlignsTimestamps(int(col.precision.Int64))),
		"column %q must align to timestamp precision %s", name, c.TimestampPrecision)
}

// listTextType reports a MariaDB character type.
func listTextType(dataType string) bool {
	switch dataType {
	case listChar, listVarchar, "tinytext", listText, "mediumtext", listLongText:
		return true
	default:
		return false
	}
}

// listTypeName returns the expected MariaDB data type of a storage kind.
func listTypeName(kind sqlbase.Kind) string {
	switch kind {
	case sqlbase.Text, sqlbase.UUID:
		return listVarchar
	case sqlbase.Integer:
		return "bigint"
	case sqlbase.Boolean:
		return "tinyint"
	case sqlbase.Float:
		return "double"
	case sqlbase.Bytes:
		return "blob"
	case sqlbase.Timestamp:
		return "datetime"
	case sqlbase.Numeric:
		return "decimal"
	default:
		return "unknown"
	}
}

// listTypeMatches reports whether a MariaDB data type stores a storage kind; an identity must be VARCHAR or CHAR.
func listTypeMatches(dataType string, kind sqlbase.Kind, id bool) bool {
	if kind == sqlbase.Text || kind == sqlbase.UUID {
		if id {
			return dataType == listVarchar || dataType == listChar
		}

		return listTextType(dataType)
	}

	if kind == sqlbase.Bytes {
		switch dataType {
		case "binary", "varbinary", "tinyblob", "blob", "mediumblob", "longblob":
			return true
		}
	}

	return dataType == listTypeName(kind)
}
