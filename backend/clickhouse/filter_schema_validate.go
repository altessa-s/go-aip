// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse

import (
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqltype"
	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// schemaExpression is a typed node of a checked filter.
type schemaExpression = sqltype.Expr[sqlbase.Field]

// schemaValidator is the ClickHouse [sqltype.Dialect] of one translation: it refuses the storage ClickHouse cannot
// compare with the listing semantics and spends the literal budget.
type schemaValidator struct {
	schema    *sqlbase.Schema
	policy    *filter.Policy
	remaining int
}

// validate types the complete filter tree before anything is rendered, so a malformed branch cannot bypass the type
// checks of another, and returns the typed tree the renderer reads. A nil node has no tree.
func (t *SchemaFilterTranslator) validate(node filter.Node) (*schemaExpression, error) {
	var err error
	if t.policy.PortableContract() {
		err = filter.Validate(node, t.policy)
	} else {
		err = t.policy.CheckFields(node)
	}

	if err != nil || node == nil {
		return nil, err
	}

	// ClickHouse arrays of the contract are one-dimensional, so a nested list is refused; every string function takes a
	// literal argument.
	c := sqltype.Checker[sqlbase.Field]{Dialect: &schemaValidator{schema: t.schema.fields, policy: t.policy, remaining: t.schema.opts.maxDataBudget},
		Policy: t.policy, TypeError: typeError, Unsupported: unsupported, Contract: true, LiteralStringArgs: true}

	return c.Check(node)
}

// fieldKind returns the operand kind of a declared field, rejecting the storage ClickHouse cannot compare with the
// listing semantics: whole JSON values, UUID columns, and decimal or bytes JSON paths.
func fieldKind(f sqlbase.Field) (sqlbase.Kind, error) {
	switch {
	case f.Kind() == sqlbase.JSON || f.Kind() == sqlbase.UUID:
		return sqlbase.Unknown, coreerrs.Wrap(filter.ErrUnsupportedOperation, "ClickHouse contract requires scalar, array or typed JSON path storage")
	case f.IsJSONPath() && (f.Kind() == sqlbase.Numeric || f.Kind() == sqlbase.Bytes):
		return sqlbase.Unknown, coreerrs.Wrap(filter.ErrUnsupportedOperation, "ClickHouse cannot extract an exact decimal or bytes from a JSON path")
	default:
		return f.Kind(), nil
	}
}

func (c *schemaValidator) Field(name string) (sqlbase.Field, sqlbase.Kind, bool, error) {
	f, err := c.schema.Field(name)
	if err != nil {
		return sqlbase.Field{}, sqlbase.Unknown, false, err
	}

	kind, err := fieldKind(f)
	if err != nil {
		return sqlbase.Field{}, sqlbase.Unknown, false, err
	}

	return f, kind, f.IsArray(), nil
}

func (c *schemaValidator) Hint(name string) (sqlbase.Kind, error) {
	f, err := c.schema.Field(name)
	if err != nil {
		return sqlbase.Unknown, err
	}

	return fieldKind(f)
}

func (*schemaValidator) InferKind(v any) sqlbase.Kind { return inferKind(v) }

// inferKind infers the kind of a literal; a JSON number has none in ClickHouse.
func inferKind(v any) sqlbase.Kind {
	if _, ok := v.(json.Number); ok {
		return sqlbase.Unknown
	}

	return sqltype.InferKind(v)
}

func (c *schemaValidator) Literal(x *filter.LiteralNode, kind sqlbase.Kind) (any, error) {
	if err := c.validateLiteral(x, kind); err != nil {
		return nil, err
	}

	return x.Value, nil
}

func (*schemaValidator) Comparison(filter.Operator, *schemaExpression, *schemaExpression) error {
	return nil
}

func (c *schemaValidator) Regex(_ *schemaExpression, pattern string) error {
	if c.policy.ContractRuleEnabled(filter.PortableRegex) {
		return sqltype.CheckPortableRegex(pattern)
	}

	return nil
}

// validateLiteral checks that a literal is representable in its ClickHouse operand type and spends its size from the
// data budget.
func (c *schemaValidator) validateLiteral(x *filter.LiteralNode, kind sqlbase.Kind) error {
	if err := c.spend(1); err != nil {
		return err
	}

	switch v := x.Value.(type) {
	case string:
		if err := c.spend(len(v)); err != nil {
			return err
		}

		if kind == sqlbase.Text && (!utf8.ValidString(v) || strings.ContainsRune(v, 0)) {
			return typeError()
		}
	case []byte:
		return c.spend(len(v))
	case uint64:
		if kind == sqlbase.Integer && v > math.MaxInt64 {
			return typeError()
		}
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return typeError()
		}
	case time.Time:
		if v.Before(minDateTime64) || !v.Before(maxDateTime64) {
			return coreerrs.Wrapf(filter.ErrFieldTypeMismatch, "timestamp %s is outside the DateTime64 range", v.UTC().Format(time.RFC3339))
		}
	default:
	}

	if kind == sqlbase.Numeric {
		d, err := c.policy.DecimalOperand(x, x.Value)
		if err != nil {
			return err
		}

		return c.spend(d.NumDigits())
	}

	return nil
}

func (c *schemaValidator) spend(n int) error {
	if n < 0 || n > c.remaining {
		return filter.ErrDataLimitExceeded
	}

	c.remaining -= n

	return nil
}

func typeError() error {
	return coreerrs.Wrap(filter.ErrFieldTypeMismatch, "incompatible SQL operand types")
}

func unsupported() error {
	return coreerrs.Wrap(filter.ErrUnsupportedOperation, "unsupported ClickHouse expression")
}

// The DateTime64 range: 1900-01-01 00:00:00 through 2299-12-31 23:59:59.999999999 UTC; at nanosecond precision
// (DateTime64(9), an Int64 of nanoseconds) it ends before 2262-04-11 23:47:16.854775807 UTC.
var (
	minDateTime64      = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	maxDateTime64      = time.Date(2300, 1, 1, 0, 0, 0, 0, time.UTC)
	maxDateTime64Nanos = time.Date(2262, 4, 11, 23, 47, 16, 0, time.UTC)
)
