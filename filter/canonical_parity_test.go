// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-atlas-core/encoding/keys"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/internal/literalvalue"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

// legacyCanonical is the encoder filter.Canonical replaced, kept as the reference its output must match byte for byte: page
// tokens issued before the change bind the canonical digest of their filter. It clones the tree, then composes every node's
// parts with keys.Compose, recursively.
func legacyCanonical(n filter.Node) (string, error) {
	snapshot, err := filter.Clone(n)
	if err != nil {
		return "", err
	}

	return legacyEncode(snapshot), nil
}

func legacyEncode(n filter.Node) string {
	switch x := n.(type) {
	case nil:
		return ""
	case *filter.LiteralNode:
		return keys.Compose("L", literalvalue.Canonical(x.Value), x.Text)
	case *filter.IdentNode:
		return keys.Compose("I", x.Name)
	case *filter.BinaryOpNode:
		return keys.Compose("B", x.Op.String(), legacyEncode(x.Left), legacyEncode(x.Right))
	case *filter.UnaryOpNode:
		return keys.Compose("U", x.Op.String(), legacyEncode(x.Operand))
	case *filter.CallNode:
		parts := []string{"C", x.Op.String(), legacyEncode(x.Target)}
		for _, arg := range x.Args {
			parts = append(parts, legacyEncode(arg))
		}

		return keys.Compose(parts...)
	case *filter.ListNode:
		parts := []string{"A"}
		for _, el := range x.Elements {
			parts = append(parts, legacyEncode(el))
		}

		return keys.Compose(parts...)
	default:
		return keys.Compose("?", fmt.Sprintf("%T", n))
	}
}

// requireLegacyCanonical asserts that filter.Canonical and the legacy encoder agree on n: the same bytes, or the same error.
func requireLegacyCanonical(t testing.TB, n filter.Node, label string) {
	t.Helper()

	want, wantErr := legacyCanonical(n)
	got, err := filter.Canonical(n)

	if wantErr != nil {
		require.Error(t, err, label)
		require.Equal(t, wantErr.Error(), err.Error(), label)

		for _, sentinel := range []error{filter.ErrInvalidExpression, filter.ErrUnsupportedType} {
			require.Equal(t, errors.Is(wantErr, sentinel), errors.Is(err, sentinel), "%s: errors.Is(%v)", label, sentinel)
		}

		return
	}

	require.NoError(t, err, label)
	require.Equal(t, want, got, label)
}

// conformanceExpressions returns the filter expressions of the conformance suite, read from its source.
func conformanceExpressions(t *testing.T) []string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), "filtertest/conformance_cases.go", nil, parser.SkipObjectResolution)
	require.NoError(t, err)

	var exprs []string

	ast.Inspect(file, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}

		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "expr" {
			if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				expr, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)

				exprs = append(exprs, expr)
			}
		}

		return true
	})

	return exprs
}

// TestCanonicalMatchesLegacyEncoder encodes every filter of the conformance suite, and trees the parser cannot build, with
// filter.Canonical and the legacy encoder.
func TestCanonicalMatchesLegacyEncoder(t *testing.T) {
	t.Parallel()

	p, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(t, err)

	parsed := 0

	for _, expr := range append(conformanceExpressions(t),
		`(name == "John" || name == "Jane") && age >= 18 && status in ["active", "pending"] && !(x == 1) && y.startsWith("a")`,
		`a.substring(1, 2) == "x" && size(tags) > 3 && has(address.city) && ts > timestamp("2026-01-02T03:04:05.123456789Z")`,
		`b == b"\x00\xff" && f == 1.00000000000000001 && u == 18446744073709551615u && n == null && t != true`,
		`name.matches("^a.*$") || !(score < -0.5) && "x" in tags && a in []`,
	) {
		node, err := p.Parse(t.Context(), expr)
		if err != nil {
			continue // a case that needs its own parser options
		}

		parsed++

		requireLegacyCanonical(t, node, expr)
	}

	require.Greater(t, parsed, 90, "the conformance expressions must parse")

	at := time.Date(2026, 3, 1, 12, 0, 0, 123_000_000, time.FixedZone("UTC+3", 3*3600))

	type named string

	type pair struct {
		A int8
		B [2]uint16
	}

	ident := &filter.IdentNode{Name: "a"}
	literals := []any{
		nil, "", "x:y", "ünïcode", true, false, 0, -1, math.MaxInt, math.MinInt, int64(0), int64(math.MinInt64),
		uint64(math.MaxUint64), 0.0, math.Copysign(0, -1), 0.1, 1e300, -1e-300, math.Inf(1), math.Inf(-1), math.NaN(),
		float32(1.5), int8(-3), uint16(7), int32(9), uint(3), complex(1, -2), named("n"), pair{A: 1, B: [2]uint16{2, 3}},
		[3]string{"a", "", "c"}, struct{}{}, decimal.RequireFromString("1.2300"), at, time.Time{},
		time.Date(12345, 1, 1, 0, 0, 0, 0, time.UTC), []byte{}, []byte(nil), []byte("\x00\xff"), uintptr(1),
	}

	for _, value := range literals {
		label := fmt.Sprintf("literal %T %#v", value, value)
		requireLegacyCanonical(t, &filter.LiteralNode{Value: value}, label)
		requireLegacyCanonical(t, &filter.LiteralNode{Value: value, Text: "0.10000000000000000001"}, label)
		requireLegacyCanonical(t, &filter.BinaryOpNode{Op: filter.OpEqual, Left: ident, Right: &filter.LiteralNode{Value: value}}, label)
	}

	eq := &filter.BinaryOpNode{Op: filter.OpEqual, Left: ident, Right: &filter.LiteralNode{Value: int64(1)}}
	long := &filter.LiteralNode{Value: string(make([]byte, 1<<16)), Text: string(make([]byte, 999))}
	trees := map[string]filter.Node{
		"nil":                        nil,
		"nil binary operands":        &filter.BinaryOpNode{Op: filter.OpAnd},
		"nil unary operand":          &filter.UnaryOpNode{Op: filter.OpNot},
		"nil call target and args":   &filter.CallNode{Op: filter.OpContains, Args: []filter.Node{nil, ident, nil}},
		"empty call":                 &filter.CallNode{Op: filter.OpHas},
		"nil list elements":          &filter.ListNode{Elements: []filter.Node{nil, eq, nil}},
		"empty list":                 &filter.ListNode{},
		"unknown operator":           &filter.BinaryOpNode{Op: filter.Operator(200), Left: ident, Right: ident},
		"shared subtree":             &filter.BinaryOpNode{Op: filter.OpOr, Left: eq, Right: &filter.UnaryOpNode{Op: filter.OpNot, Operand: eq}},
		"long parts":                 &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: string(make([]byte, 100))}, Right: long},
		"extension":                  &filter.BinaryOpNode{Op: filter.OpAnd, Left: loweredPredicate{eq}, Right: eq},
		"extension root":             loweredPredicate{&filter.ListNode{Elements: []filter.Node{eq, loweredPredicate{ident}}}},
		"extension to itself":        &filter.UnaryOpNode{Op: filter.OpNot, Operand: selfLowering{}},
		"extension error":            &filter.UnaryOpNode{Op: filter.OpNot, Operand: failingLowering{}},
		"custom node":                &filter.BinaryOpNode{Op: filter.OpAnd, Left: eq, Right: &wrapNode{child: eq}},
		"typed nil literal":          &filter.UnaryOpNode{Op: filter.OpNot, Operand: (*filter.LiteralNode)(nil)},
		"typed nil ident":            &filter.ListNode{Elements: []filter.Node{(*filter.IdentNode)(nil)}},
		"typed nil binary":           &filter.UnaryOpNode{Op: filter.OpNot, Operand: (*filter.BinaryOpNode)(nil)},
		"typed nil unary":            (*filter.UnaryOpNode)(nil),
		"typed nil call":             &filter.ListNode{Elements: []filter.Node{eq, (*filter.CallNode)(nil)}},
		"typed nil list":             &filter.CallNode{Op: filter.OpContains, Target: ident, Args: []filter.Node{(*filter.ListNode)(nil)}},
		"mutable literal":            &filter.BinaryOpNode{Op: filter.OpIn, Left: ident, Right: &filter.LiteralNode{Value: []int{1}}},
		"first error wins":           &filter.BinaryOpNode{Op: filter.OpAnd, Left: &filter.LiteralNode{Value: map[string]int{}}, Right: &wrapNode{}},
		"extension before error":     &filter.BinaryOpNode{Op: filter.OpAnd, Left: loweredPredicate{eq}, Right: &filter.LiteralNode{Value: []int{1}}},
		"error before extension":     &filter.BinaryOpNode{Op: filter.OpAnd, Left: &wrapNode{}, Right: failingLowering{}},
		"extension lowers to error":  loweredPredicate{&filter.LiteralNode{Value: &at}},
		"extension lowers to nil":    &filter.UnaryOpNode{Op: filter.OpNot, Operand: loweredPredicate{}},
		"extension lowers to custom": loweredPredicate{&wrapNode{child: eq}},
	}

	deep := filter.Node(eq)
	for i := range 200 {
		deep = &filter.BinaryOpNode{Op: filter.OpAnd, Left: deep, Right: &filter.LiteralNode{Value: int64(i)}}
	}

	trees["deep"] = deep

	for name, tree := range trees {
		requireLegacyCanonical(t, tree, name)
	}
}

type selfLowering struct{ *filter.IdentNode }

func (n selfLowering) Lower() (filter.Node, error) { return n, nil }

type failingLowering struct{ *filter.IdentNode }

func (failingLowering) Lower() (filter.Node, error) {
	return nil, fmt.Errorf("lowering failed: %w", filter.ErrInvalidExpression)
}

// FuzzCanonicalParse parses arbitrary input and checks that filter.Canonical encodes the tree as the legacy encoder does.
func FuzzCanonicalParse(f *testing.F) {
	f.Add(`name == "John"`)
	f.Add(`age > 18 && active == true || !(score <= -1.5e10)`)
	f.Add(`status in ["a", "b", null]`)
	f.Add(`name.contains("oh") && startsWith(name, "J") && size(tags) == 2u`)
	f.Add(`ts < timestamp("2026-01-01T00:00:00Z") && b == b"\x01"`)
	f.Add(`f == 1.00000000000000001 && s.substring(0, 3) == "abc" && has(a.b.c)`)

	p, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(f, err)

	f.Fuzz(func(t *testing.T, input string) {
		node, err := p.Parse(t.Context(), input)
		if err != nil {
			return
		}

		requireLegacyCanonical(t, node, input)
	})
}

// FuzzCanonicalLiterals encodes arbitrary literal values, spellings and names with filter.Canonical and the legacy encoder.
func FuzzCanonicalLiterals(f *testing.F) {
	f.Add("name", "x", "", int64(-1), uint64(1), 0.5, true, []byte("b"), int64(0))
	f.Add("", "", "1.00000000000000001", int64(math.MaxInt64), uint64(math.MaxUint64), math.Inf(-1), false, []byte(nil), int64(-1))

	f.Fuzz(func(t *testing.T, name, s, text string, i int64, u uint64, fl float64, b bool, bs []byte, nanos int64) {
		values := []any{s, i, u, fl, b, bs, int(i), time.Unix(0, nanos).In(time.FixedZone("z", int(i%86400))), nil}
		elements := make([]filter.Node, 0, len(values))

		for _, value := range values {
			elements = append(elements, &filter.LiteralNode{Value: value, Text: text})
		}

		requireLegacyCanonical(t, &filter.CallNode{
			Op:     filter.OpContains,
			Target: &filter.IdentNode{Name: name},
			Args:   []filter.Node{&filter.ListNode{Elements: elements}, &filter.LiteralNode{Value: s, Text: text}},
		}, fmt.Sprintf("%q %q %q", name, s, text))
	})
}
