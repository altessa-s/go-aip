// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqltype_test

import (
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqltype"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

var (
	errTypeCheck   = errors.New("incompatible operands")
	errUnsupported = errors.New("unsupported expression")
	errField       = errors.New("unusable field")
	errLiteral     = errors.New("bad literal")
	errComparison  = errors.New("bad comparison")
	errRegex       = errors.New("bad regex")
)

type testField struct {
	kind  sqlbase.Kind
	array bool
}

// dialect is a [sqltype.Dialect] over a fixed set of fields. The field "broken" fails to resolve as an operand, and the
// literal "reject" fails validation.
type dialect struct{}

var testFields = map[string]testField{
	"name":   {kind: sqlbase.Text},
	"tags":   {kind: sqlbase.Text, array: true},
	"age":    {kind: sqlbase.Integer},
	"score":  {kind: sqlbase.Float},
	"price":  {kind: sqlbase.Numeric},
	"doc":    {kind: sqlbase.JSON},
	"active": {kind: sqlbase.Boolean},
	"data":   {kind: sqlbase.Bytes},
	"flags":  {kind: sqlbase.Boolean, array: true},
	"broken": {kind: sqlbase.Text},
}

func (dialect) Field(name string) (testField, sqlbase.Kind, bool, error) {
	f, ok := testFields[name]
	if !ok || name == "broken" {
		return testField{}, sqlbase.Unknown, false, errField
	}

	return f, f.kind, f.array, nil
}

func (dialect) Hint(name string) (sqlbase.Kind, error) {
	f, ok := testFields[name]
	if !ok {
		return sqlbase.Unknown, errField
	}

	return f.kind, nil
}

func (dialect) InferKind(v any) sqlbase.Kind { return sqltype.InferKind(v) }

func (dialect) Literal(x *filter.LiteralNode, _ sqlbase.Kind) (any, error) {
	if x.Value == "reject" {
		return nil, errLiteral
	}

	return x.Value, nil
}

func (dialect) Comparison(op filter.Operator, a, _ *sqltype.Expr[testField]) error {
	if op == filter.OpGT && a.Kind == sqlbase.JSON {
		return errComparison
	}

	return nil
}

func (dialect) Regex(_ *sqltype.Expr[testField], pattern string) error {
	if pattern == "bad" {
		return errRegex
	}

	return nil
}

func checker(t testing.TB, configure func(*sqltype.Checker[testField])) *sqltype.Checker[testField] {
	t.Helper()

	c := &sqltype.Checker[testField]{
		Dialect:            dialect{},
		Policy:             filtertest.MustPolicy(t, filter.WithAnyField(true)),
		TypeError:          func() error { return errTypeCheck },
		Unsupported:        func() error { return errUnsupported },
		Contract:           true,
		MaxArrayDimensions: 2,
	}
	if configure != nil {
		configure(c)
	}

	return c
}

func flat(c *sqltype.Checker[testField]) { c.MaxArrayDimensions = 0 }

func mustPolicy(opts ...filter.PolicyOption) *filter.Policy {
	policy, err := filter.NewPolicy(opts...)
	if err != nil {
		panic(err)
	}

	return policy
}

var (
	// decimalPolicy declares price a decimal field whose operands never parse.
	decimalPolicy = mustPolicy(filter.WithAnyField(true), filter.WithFieldTypes(map[string]filter.FieldKind{"price": filter.FieldKindDecimal}),
		filter.WithDecimalParser(func(filter.Node, any) (decimal.Decimal, error) { return decimal.Decimal{}, errLiteral }))
	// enumPolicy declares the enum values of age.
	enumPolicy = mustPolicy(filter.WithAnyField(true), filter.WithEnumValues(map[string][]int64{"age": {1, 2}}))
)

func decimals(c *sqltype.Checker[testField]) {
	c.NormalizeDecimals = true
	c.Policy = decimalPolicy
}

func enums(c *sqltype.Checker[testField]) { c.Policy = enumPolicy }

func TestCheckErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		expr      string
		configure func(*sqltype.Checker[testField])
		err       error
	}{
		{expr: `name == "a"`},
		{expr: `name`, err: filter.ErrInvalidExpression},
		{expr: `tags`, err: filter.ErrInvalidExpression},
		{expr: `broken == "a"`, err: errField},
		{expr: `missing == "a"`, err: errField},
		{expr: `"a" == missing`, err: errField},
		{expr: `!name`, err: errTypeCheck},
		{expr: `!(name == "a")`},
		{expr: `age == "a"`, err: errTypeCheck},
		{expr: `age == 1.5`, err: errTypeCheck},
		{expr: `score == 1`},
		{expr: `name == "reject"`, err: errLiteral},
		{expr: `name in ["a", 1]`, err: errTypeCheck},
		{expr: `age in [1, 2.5]`},
		{expr: `name in [["a"], ["b"]]`},
		{expr: `name in [["a"], ["b", "c"]]`, err: errTypeCheck},
		{expr: `name in ["a", ["b"]]`, err: errTypeCheck},
		{expr: `name in [[["a"]]]`, err: errTypeCheck},
		{expr: `name in [tags]`, err: errUnsupported},
		{expr: `name in [["a"]]`, configure: flat, err: errUnsupported},
		{expr: `name in [tags]`, configure: flat, err: errUnsupported},
		{expr: `name in ["a"]`, configure: flat},
		{expr: `name in "a"`, err: errTypeCheck},
		{expr: `tags in ["a"]`},
		{expr: `tags in ["a"]`, configure: func(c *sqltype.Checker[testField]) { c.Contract = false }, err: errTypeCheck},
		{expr: `name == "a" && name`, err: errTypeCheck},
		{expr: `name < null`, err: errUnsupported},
		{expr: `name == null`},
		{expr: `name == ["a"]`, err: errUnsupported},
		{expr: `null == null`},
		{expr: `null in [null]`},
		{expr: `doc > 1`, err: errComparison},
		{expr: `size(name) == 1`},
		{expr: `size(tags) == 1`},
		{expr: `size(doc) == 1`},
		{expr: `size(age) == 1`, err: errTypeCheck},
		{expr: `name.contains("a")`},
		{expr: `name.contains(name)`},
		{expr: `name.contains(name)`, configure: func(c *sqltype.Checker[testField]) { c.LiteralStringArgs = true },
			err: filter.ErrUnsupportedOperation},
		{expr: `age.contains("a")`, err: errTypeCheck},
		{expr: `name.contains(1)`, err: errTypeCheck},
		{expr: `name.contains(tags)`, err: errTypeCheck},
		{expr: `doc.contains(doc)`},
		{expr: `doc.contains(tags)`, err: errTypeCheck},
		{expr: `name.matches("a")`},
		{expr: `name.matches("bad")`, err: errRegex},
		{expr: `name.matches(name)`, err: errUnsupported},
		{expr: `name.matches("(")`, err: filter.ErrInvalidRegex},
		{expr: `name.substring(1) == "a"`},
		{expr: `name.substring(1, 2) == "a"`},
		{expr: `name.substring(-1) == "a"`, err: filter.ErrInvalidExpression},
		{expr: `tags.substring(1) == "a"`, err: filter.ErrUnsupportedOperation},
		{expr: `"a".substring(1) == "a"`, err: filter.ErrUnsupportedOperation},
		{expr: `age.substring(1) == "a"`, err: errTypeCheck},
		{expr: `size(name.substring(1)) == 1`, err: filter.ErrUnsupportedOperation},
		{expr: `name.contains(name.substring(1))`, err: filter.ErrUnsupportedOperation},
		{expr: `age.contains(name.substring(1))`, err: errTypeCheck},
		{expr: `age.contains(name.substring(1))`, configure: func(c *sqltype.Checker[testField]) { c.SubstringArgFirst = true },
			err: filter.ErrUnsupportedOperation},
		{expr: `price == 1.5`, configure: func(c *sqltype.Checker[testField]) { c.NormalizeDecimals = true }},
		{expr: `price == 1.5`, configure: decimals, err: errLiteral},
		{expr: `1.5 == price`, configure: decimals, err: errLiteral},
		{expr: `age in [1, name]`, err: errTypeCheck},
		{expr: `name in [["a"], "b"]`, err: errTypeCheck},
		{expr: `flags && active`, err: errTypeCheck},
		{expr: `age == 3`, configure: enums, err: filter.ErrEnumValueNotAllowed},
		{expr: `3 == age`, configure: enums, err: filter.ErrEnumValueNotAllowed},
		{expr: `age == 1`, configure: enums},
		{expr: `doc == "a"`},
		{expr: `missing.contains("a")`, err: errField},
		{expr: `doc.contains(missing)`, err: errField},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			_, err := checker(t, tc.configure).Check(filtertest.MustParseFilter(t, tc.expr))
			if tc.err == nil {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, tc.err)
		})
	}
}

// custom is a node of a kind the checker does not know.
type custom struct{}

func (custom) Kind() filter.NodeKind              { return filter.NodeKindCall }
func (custom) Accept(filter.Visitor) (any, error) { return nil, nil }
func (custom) Children() iter.Seq[filter.Node]    { return func(func(filter.Node) bool) {} }

func TestCheckNodeShapes(t *testing.T) {
	t.Parallel()

	name := &filter.IdentNode{Name: "name"}

	for _, tc := range []struct {
		name string
		node filter.Node
		err  error
	}{
		{name: "nil operand", node: &filter.BinaryOpNode{Op: filter.OpEqual, Left: name}, err: filter.ErrInvalidExpression},
		{name: "unary operator", node: &filter.UnaryOpNode{Op: filter.OpAnd, Operand: name}, err: errUnsupported},
		{name: "binary operator", node: &filter.BinaryOpNode{Op: filter.OpContains, Left: name, Right: name}, err: errUnsupported},
		{name: "call operator", node: &filter.CallNode{Op: filter.OpEqual, Target: name}, err: errUnsupported},
		{name: "has argument", node: &filter.CallNode{Op: filter.OpHas, Target: name, Args: []filter.Node{name}}, err: errUnsupported},
		{name: "string function arguments", node: &filter.CallNode{Op: filter.OpContains, Target: name}, err: errTypeCheck},
		{name: "target as first argument", node: &filter.CallNode{Op: filter.OpHas, Args: []filter.Node{name}}},
		{name: "has literal", node: &filter.CallNode{Op: filter.OpHas, Target: &filter.LiteralNode{Value: "a"}}, err: errUnsupported},
		{name: "custom node", node: &filter.UnaryOpNode{Op: filter.OpNot, Operand: custom{}}, err: errUnsupported},
		{name: "matches non-string", node: &filter.CallNode{Op: filter.OpMatches, Target: name,
			Args: []filter.Node{&filter.LiteralNode{}}}, err: errTypeCheck},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := checker(t, nil).Check(tc.node)
			if tc.err == nil {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, tc.err)
		})
	}
}

func TestCheckTree(t *testing.T) {
	t.Parallel()

	check := func(expr string) *sqltype.Expr[testField] {
		t.Helper()

		root, err := checker(t, nil).Check(filtertest.MustParseFilter(t, expr))
		require.NoError(t, err)

		return root
	}

	sub := check(`name.substring(1, 3) == "a"`).Children[0]
	assert.True(t, sub.IsSubstring())
	assert.Equal(t, sqltype.Span{Start: 1, Length: 2, Guard: 3}, sub.Span)
	assert.True(t, sub.Children[0].IsField())
	assert.Equal(t, testFields["name"], sub.Children[0].Field)

	list := check(`name in [["a", null], ["b", "c"]]`).Children[1]
	assert.Equal(t, []sqltype.Dimension{{Length: 2, LowerBound: 1}, {Length: 2, LowerBound: 1}}, list.Dimensions)
	assert.Equal(t, sqlbase.Text, list.Children[0].Kind)
	assert.True(t, list.Children[0].Children[1].IsNull())

	matches := check(`name.matches("^a")`)
	require.NotNil(t, matches.Matcher)
	assert.True(t, matches.Matcher.MatchString("ab"))
	assert.True(t, matches.RE2)

	cmp := check(`null == name`)
	assert.True(t, cmp.Children[0].IsNull())
	assert.Equal(t, sqlbase.Text, cmp.Children[0].Kind)
	assert.Equal(t, &filter.IdentNode{Name: "name"}, cmp.Children[1].Node)

	assert.Equal(t, sqlbase.Text, check(`null in [null]`).Children[1].Kind)
}

func TestKindHelpers(t *testing.T) {
	t.Parallel()

	assert.True(t, sqltype.Compatible(sqlbase.Integer, sqlbase.Numeric))
	assert.True(t, sqltype.Compatible(sqlbase.Unknown, sqlbase.Text))
	assert.False(t, sqltype.Compatible(sqlbase.Text, sqlbase.Bytes))
	assert.Equal(t, sqlbase.Float, sqltype.ComparisonKind(sqlbase.Numeric, sqlbase.Float))
	assert.Equal(t, sqlbase.Numeric, sqltype.ComparisonKind(sqlbase.Integer, sqlbase.Numeric))
	assert.Equal(t, sqlbase.Text, sqltype.ComparisonKind(sqlbase.Unknown, sqlbase.Text))
	assert.Equal(t, sqlbase.Integer, sqltype.ComparisonKind(sqlbase.Integer, sqlbase.Unknown))

	scalar := &sqltype.Expr[testField]{Tag: filter.NodeKindIdent, Kind: sqlbase.Integer}
	sqltype.SetListKind(scalar, sqlbase.Text)
	assert.Equal(t, sqlbase.Integer, scalar.Kind)
}

func TestCheckPortableRegex(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		pattern string
		err     error
	}{
		{pattern: `^a\.b.*c$`},
		{pattern: "(?i)ab"},
		{pattern: "^a[bc]+$", err: filter.ErrUnsupportedOperation},
		{pattern: "(?i)é", err: filter.ErrUnsupportedOperation},
		{pattern: `\bx`, err: filter.ErrUnsupportedOperation},
		{pattern: "(?i)(", err: filter.ErrInvalidRegex},
		{pattern: "(", err: filter.ErrInvalidRegex},
		// The reported alternation chains: POSIX syntax, but outside the portable subset filter.Validate applies.
		{pattern: strings.Repeat("(a|a)", 25) + "$", err: filter.ErrUnsupportedOperation},
		{pattern: strings.Repeat("(.|a)", 25) + "$", err: filter.ErrUnsupportedOperation},
		{pattern: "^" + strings.Repeat("(a|ab|abc)", 16) + "$", err: filter.ErrUnsupportedOperation},
	} {
		err := sqltype.CheckPortableRegex(tc.pattern)
		if tc.err == nil {
			require.NoError(t, err, tc.pattern)
			continue
		}

		require.ErrorIs(t, err, tc.err, tc.pattern)
	}
}
