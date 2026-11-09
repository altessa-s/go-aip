// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqltype_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqltype"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/parser"
)

// column is the field descriptor of the example dialect.
type column struct{ name string }

// exampleDialect is a minimal [sqltype.Dialect] of a SQL database outside this repository: two declared columns, the
// default literal kinds and the portable regex rule.
type exampleDialect struct{}

var exampleColumns = map[string]sqlbase.Kind{"age": sqlbase.Integer, "name": sqlbase.Text}

func (exampleDialect) Field(name string) (column, sqlbase.Kind, bool, error) {
	kind, ok := exampleColumns[name]
	if !ok {
		return column{}, sqlbase.Unknown, false, filter.ErrFieldNotAllowed
	}

	return column{name: name}, kind, false, nil
}

func (exampleDialect) Hint(name string) (sqlbase.Kind, error) { return exampleColumns[name], nil }

func (exampleDialect) InferKind(v any) sqlbase.Kind { return sqltype.InferKind(v) }

func (exampleDialect) Literal(x *filter.LiteralNode, _ sqlbase.Kind) (any, error) {
	return x.Value, nil
}

func (exampleDialect) Comparison(filter.Operator, *sqltype.Expr[column], *sqltype.Expr[column]) error {
	return nil
}

func (exampleDialect) Regex(_ *sqltype.Expr[column], pattern string) error {
	return sqltype.CheckPortableRegex(pattern)
}

// A dialect types a filter once and renders SQL from the typed tree.
func ExampleChecker() {
	p, err := parser.New()
	if err != nil {
		panic(err)
	}

	node, err := p.Parse(context.Background(), `age >= 18 && name.startsWith("a")`)
	if err != nil {
		panic(err)
	}

	policy, err := filter.NewPolicy(filter.WithAllowedFields("age", "name"))
	if err != nil {
		panic(err)
	}

	// A Checker holds per-call state: one per filter.
	newChecker := func() *sqltype.Checker[column] {
		return &sqltype.Checker[column]{
			Dialect: exampleDialect{}, Policy: policy,
			TypeError:   func() error { return filter.ErrFieldTypeMismatch },
			Unsupported: func() error { return filter.ErrUnsupportedOperation },
		}
	}

	root, err := newChecker().Check(node)
	if err != nil {
		panic(err)
	}

	comparison := root.Children[0]
	fmt.Println(comparison.Children[0].Field.name, comparison.Children[1].Value)

	_, err = newChecker().Check(mustParse(p, `name > 18`))
	fmt.Println(errors.Is(err, filter.ErrFieldTypeMismatch))
	// Output:
	// age 18
	// true
}

func mustParse(p *parser.Parser, expression string) filter.Node {
	node, err := p.Parse(context.Background(), expression)
	if err != nil {
		panic(err)
	}

	return node
}
