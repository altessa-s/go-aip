// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb

import (
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqltype"
	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// contractExpression is a typed node of a checked filter.
type contractExpression = sqltype.Expr[sqlbase.Field]

// contractValidator is the MariaDB [sqltype.Dialect] of one translation. Validation stays with the dialect: the sqlbase
// schema describes storage only. It refuses JSON, UUID and JSON path storage and spends the literal budget.
type contractValidator struct {
	options   *schemaOptions
	schema    *sqlbase.Schema
	policy    *filter.Policy
	remaining int
}

// validate types every branch of node before rendering, so a malformed expression cannot bypass the type checks, and
// returns the typed tree the renderer reads. A nil node has no tree.
func (t *SchemaFilterTranslator) validate(node filter.Node) (*contractExpression, error) {
	var err error
	if t.policy.PortableContract() {
		err = filter.Validate(node, t.policy)
	} else {
		err = t.policy.CheckFields(node)
	}

	if err != nil || node == nil {
		return nil, err
	}

	o := &t.schema.opts
	d := &contractValidator{schema: t.schema.fields, policy: t.policy, remaining: o.maxDataBudget, options: o}
	c := sqltype.Checker[sqlbase.Field]{Dialect: d, Policy: t.policy, TypeError: typeError, Unsupported: unsupported, Contract: true,
		MaxArrayDimensions: o.maxArrayDimensions}

	return c.Check(node)
}

func (c *contractValidator) Field(name string) (sqlbase.Field, sqlbase.Kind, bool, error) {
	f, err := c.schema.Field(name)
	if err != nil {
		return sqlbase.Field{}, sqlbase.Unknown, false, err
	}

	if f.IsJSONPath() || f.Kind() == sqlbase.JSON || f.Kind() == sqlbase.UUID {
		return sqlbase.Field{}, sqlbase.Unknown, false, unsupported()
	}

	return f, f.Kind(), f.IsArray(), nil
}

func (c *contractValidator) Hint(name string) (sqlbase.Kind, error) {
	f, err := c.schema.Field(name)
	return f.Kind(), err
}

func (*contractValidator) InferKind(v any) sqlbase.Kind { return sqltype.InferKind(v) }

func (c *contractValidator) Literal(x *filter.LiteralNode, kind sqlbase.Kind) (any, error) {
	value := x.Value
	if kind == sqlbase.Numeric && c.policy.PortableContract() {
		parsed, err := c.policy.DecimalOperand(x, value)
		if err != nil {
			return nil, err
		}

		value = parsed
	} else if x.Text != "" && kind == sqlbase.Numeric {
		value = x.Text
	}

	if err := c.validateLiteral(value, kind); err != nil {
		return nil, err
	}

	return value, nil
}

func (*contractValidator) Comparison(filter.Operator, *contractExpression, *contractExpression) error {
	return nil
}

func (c *contractValidator) Regex(_ *contractExpression, pattern string) error {
	if c.policy.ContractRuleEnabled(filter.PortableRegex) {
		return sqltype.CheckPortableRegex(pattern)
	}

	return nil
}

func typeError() error {
	return coreerrs.Wrap(filter.ErrFieldTypeMismatch, "incompatible SQL operand types")
}

func unsupported() error {
	return coreerrs.Wrap(filter.ErrUnsupportedOperation, "unsupported SQL expression")
}
