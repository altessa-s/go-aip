// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/listing"
)

// listColumn is a parsed system.columns type: the innermost type name and parameters with its wrappers.
type listColumn struct {
	base, args             string
	nullable, array, nulls bool
}

// listSchemaChecker checks the system.columns metadata of a listing table.
type listSchemaChecker struct{ config sqlbase.ListSchemaConfig }

func checkListSchema(ctx context.Context, db *sql.DB, c sqlbase.ListSchemaConfig) (err error) {
	qualifier := sqlbase.Qualifier(c.Table)

	rows, err := db.QueryContext(ctx, `SELECT name, type FROM system.columns
		WHERE database = if(? = '', currentDatabase(), ?) AND table = ?`, qualifier, qualifier, sqlbase.Unqualified(c.Table))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()

	got := make(map[string]listColumn)

	for rows.Next() {
		var name, typ string
		if err = rows.Scan(&name, &typ); err != nil {
			return err
		}

		got[name] = parseListColumn(typ)
	}

	if err = rows.Err(); err != nil {
		return err
	}

	_, err = sqlbase.CheckListColumns(ctx, db, c, got, listSchemaChecker{c})

	return err
}

// CheckScopeColumn requires a NOT NULL scalar scope column.
func (s listSchemaChecker) CheckScopeColumn(ctx context.Context, db *sql.DB, column string, col listColumn) error {
	return s.config.Check(ctx, db, sqlbase.CheckNullability, column, col.nullable || col.array, "scope column %q must be a NOT NULL scalar",
		column)
}

// CheckColumn checks the type, decimal domain, nullability and timestamp precision of a field or identity column.
func (s listSchemaChecker) CheckColumn(ctx context.Context, db *sql.DB, name string, f listing.Field, col listColumn, scoped bool) error {
	c := s.config
	kind := c.FieldKind(f)

	if err := c.Check(ctx, db, sqlbase.CheckType, name, col.array != f.Array || !col.matches(kind), "column %q has type %q, expected %q",
		name, col.base, listTypeName(kind)); err != nil {
		return err
	}

	if kind == sqlbase.Numeric {
		precision, scale, ok := col.decimal()
		if err := c.Check(ctx, db, sqlbase.CheckDecimalDomain, name, !ok || !c.FitsDecimal(precision, scale),
			"column %q exceeds configured decimal domain", name); err != nil {
			return err
		}
	}

	if err := c.Check(ctx, db, sqlbase.CheckNullability, name, col.nulls || (!scoped && f.Presence != col.nullable),
		"column %q nullability does not match presence declaration", name); err != nil {
		return err
	}

	if kind == sqlbase.Timestamp {
		digits, ok := col.timestampDigits()
		if err := c.Check(ctx, db, sqlbase.CheckTimestampPrecision, name, !ok || !c.AlignsTimestamps(digits),
			"column %q must align to timestamp precision %s", name, c.TimestampPrecision); err != nil {
			return err
		}
	}

	return nil
}

// parseListColumn peels LowCardinality, Nullable and one Array wrapper off a ClickHouse type name. A Nullable inside the
// Array marks NULL elements.
func parseListColumn(s string) listColumn {
	var t listColumn

	for {
		s = strings.TrimSpace(s)
		switch {
		case unwrapListType(&s, "LowCardinality("):
		case unwrapListType(&s, "Nullable("):
			if t.array {
				t.nulls = true
			} else {
				t.nullable = true
			}
		case !t.array && unwrapListType(&s, "Array("):
			t.array = true
		default:
			name, args, _ := strings.Cut(s, "(")
			t.base, t.args = name, strings.TrimSuffix(args, ")")

			return t
		}
	}
}

func unwrapListType(s *string, prefix string) bool {
	if !strings.HasPrefix(*s, prefix) || !strings.HasSuffix(*s, ")") {
		return false
	}

	*s = (*s)[len(prefix) : len(*s)-1]

	return true
}

// listTypeName returns the expected ClickHouse type of a storage kind.
func listTypeName(kind sqlbase.Kind) string {
	switch kind {
	case sqlbase.Text, sqlbase.Bytes:
		return "String"
	case sqlbase.Integer:
		return "Int64"
	case sqlbase.Boolean:
		return "Bool"
	case sqlbase.Float:
		return "Float64"
	case sqlbase.Timestamp:
		return "DateTime64"
	case sqlbase.Numeric:
		return "Decimal"
	default:
		return "unknown"
	}
}

func (t listColumn) matches(kind sqlbase.Kind) bool {
	switch kind {
	case sqlbase.Timestamp:
		return t.base == "DateTime64" || t.base == "DateTime"
	case sqlbase.Numeric:
		return strings.HasPrefix(t.base, "Decimal")
	default:
		return t.base == listTypeName(kind)
	}
}

// decimal returns the precision and scale of a Decimal type.
func (t listColumn) decimal() (precision, scale int, ok bool) {
	if width, sized := listDecimalWidths[t.base]; sized {
		s, err := strconv.Atoi(strings.TrimSpace(t.args))
		return width, s, err == nil
	}

	p, s, found := strings.Cut(t.args, ",")
	if t.base != "Decimal" || !found {
		return 0, 0, false
	}

	precision, perr := strconv.Atoi(strings.TrimSpace(p))
	scale, serr := strconv.Atoi(strings.TrimSpace(s))

	return precision, scale, perr == nil && serr == nil
}

// listDecimalWidths are the precisions of the sized Decimal types.
var listDecimalWidths = map[string]int{
	"Decimal32": 9, "Decimal64": 18, "Decimal128": 38, "Decimal256": 76, //nolint:mnd // ClickHouse's fixed precisions
}

// timestampDigits returns the fractional-second digits of a DateTime or DateTime64 type.
func (t listColumn) timestampDigits() (int, bool) {
	if t.base == "DateTime" {
		return 0, true
	}

	p, _, _ := strings.Cut(t.args, ",")
	digits, err := strconv.Atoi(strings.TrimSpace(p))

	return digits, err == nil
}
