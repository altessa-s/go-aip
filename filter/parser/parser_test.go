// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package parser

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	filter "github.com/altessa-s/go-aip/filter"
	filterfuncs "github.com/altessa-s/go-aip/filter/functions"
	regexanchor "github.com/altessa-s/go-aip/filter/regexanchor"
)

func TestNew(t *testing.T) {
	t.Run("default options", func(t *testing.T) {
		p, err := New()
		require.NoError(t, err)
		require.NotNil(t, p)
		require.NotNil(t, p.cache, "expected cache to be enabled by default")
	})

	t.Run("with custom cache size", func(t *testing.T) {
		p, err := New(WithCacheSize(500))
		require.NoError(t, err)
		require.NotNil(t, p.cache, "expected cache to be enabled")
	})

	t.Run("with no cache", func(t *testing.T) {
		p, err := New(WithNoCache(true))
		require.NoError(t, err)
		require.Nil(t, p.cache, "expected cache to be nil")
	})
}

func TestParser_Parse_EmptyExpression(t *testing.T) {
	p, _ := New(WithNoCache(true))

	tests := []struct {
		name string
		expr string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"tabs", "\t\t"},
		{"newlines", "\n\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.Parse(t.Context(), tt.expr)
			require.ErrorIs(t, err, filter.ErrEmptyExpression, "Parse(%q)", tt.expr)
		})
	}
}

func TestParser_Parse_Literals(t *testing.T) {
	p, _ := New(WithNoCache(true))

	tests := []struct {
		name     string
		expr     string
		wantKind filter.NodeKind
		wantVal  any
	}{
		{"string", `"hello"`, filter.NodeKindLiteral, "hello"},
		{"int", `42`, filter.NodeKindLiteral, int64(42)},
		{"float", `3.14`, filter.NodeKindLiteral, 3.14},
		{"bool true", `true`, filter.NodeKindLiteral, true},
		{"bool false", `false`, filter.NodeKindLiteral, false},
		{"null", `null`, filter.NodeKindLiteral, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := p.Parse(t.Context(), tt.expr)
			require.NoError(t, err, "Parse(%q)", tt.expr)
			require.Equal(t, tt.wantKind, node.Kind(), "Parse(%q).Kind()", tt.expr)
			lit, ok := node.(*filter.LiteralNode)
			require.True(t, ok, "Parse(%q) did not return LiteralNode", tt.expr)
			require.Equal(t, tt.wantVal, lit.Value, "Parse(%q).Value", tt.expr)
		})
	}
}

func TestParser_Parse_Identifiers(t *testing.T) {
	p, _ := New(WithNoCache(true))

	tests := []struct {
		name     string
		expr     string
		wantName string
	}{
		{"simple", `name`, "name"},
		{"nested", `address.city`, "address.city"},
		{"deeply nested", `user.profile.address.street`, "user.profile.address.street"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := p.Parse(t.Context(), tt.expr)
			require.NoError(t, err, "Parse(%q)", tt.expr)

			ident, ok := node.(*filter.IdentNode)
			require.True(t, ok, "Parse(%q) did not return IdentNode, got %T", tt.expr, node)
			require.Equal(t, tt.wantName, ident.Name, "Parse(%q).Name", tt.expr)
		})
	}
}

func TestParser_Parse_ComparisonOperators(t *testing.T) {
	p, _ := New(WithNoCache(true))

	tests := []struct {
		name   string
		expr   string
		wantOp filter.Operator
	}{
		{"equal", `name == "John"`, filter.OpEqual},
		{"not equal", `name != "John"`, filter.OpNotEqual},
		{"less than", `age < 18`, filter.OpLT},
		{"less than or equal", `age <= 18`, filter.OpLTE},
		{"greater than", `age > 18`, filter.OpGT},
		{"greater than or equal", `age >= 18`, filter.OpGTE},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := p.Parse(t.Context(), tt.expr)
			require.NoError(t, err, "Parse(%q)", tt.expr)

			binOp, ok := node.(*filter.BinaryOpNode)
			require.True(t, ok, "Parse(%q) did not return BinaryOpNode, got %T", tt.expr, node)
			require.Equal(t, tt.wantOp, binOp.Op, "Parse(%q).Op", tt.expr)
		})
	}
}

func TestParser_Parse_LogicalOperators(t *testing.T) {
	p, _ := New(WithNoCache(true))

	tests := []struct {
		name   string
		expr   string
		wantOp filter.Operator
	}{
		{"and", `name == "John" && age >= 18`, filter.OpAnd},
		{"or", `status == "active" || status == "pending"`, filter.OpOr},
		{"not", `!active`, filter.OpNot},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := p.Parse(t.Context(), tt.expr)
			require.NoError(t, err, "Parse(%q)", tt.expr)

			switch tt.wantOp {
			case filter.OpAnd, filter.OpOr:
				binOp, ok := node.(*filter.BinaryOpNode)
				require.True(t, ok, "Parse(%q) did not return BinaryOpNode, got %T", tt.expr, node)
				require.Equal(t, tt.wantOp, binOp.Op, "Parse(%q).Op", tt.expr)
			case filter.OpNot:
				unaryOp, ok := node.(*filter.UnaryOpNode)
				require.True(t, ok, "Parse(%q) did not return UnaryOpNode, got %T", tt.expr, node)
				require.Equal(t, tt.wantOp, unaryOp.Op, "Parse(%q).Op", tt.expr)
			}
		})
	}
}

func TestParser_Parse_MembershipOperator(t *testing.T) {
	p, _ := New(WithNoCache(true))

	node, err := p.Parse(t.Context(), `status in ["active", "pending"]`)
	require.NoError(t, err)

	binOp, ok := node.(*filter.BinaryOpNode)
	require.True(t, ok, "Parse() did not return BinaryOpNode, got %T", node)
	require.Equal(t, filter.OpIn, binOp.Op)

	ident, ok := binOp.Left.(*filter.IdentNode)
	require.True(t, ok, "Left is not IdentNode, got %T", binOp.Left)
	require.Equal(t, "status", ident.Name)

	list, ok := binOp.Right.(*filter.ListNode)
	require.True(t, ok, "Right is not ListNode, got %T", binOp.Right)
	require.Len(t, list.Elements, 2)
}

func TestParser_Parse_StringFunctions(t *testing.T) {
	p, _ := New(WithNoCache(true))

	tests := []struct {
		name   string
		expr   string
		wantOp filter.Operator
	}{
		{"contains", `name.contains("oh")`, filter.OpContains},
		{"startsWith", `name.startsWith("J")`, filter.OpStartsWith},
		{"endsWith", `name.endsWith("n")`, filter.OpEndsWith},
		{"matches", `name.matches("^[A-Z].*")`, filter.OpMatches},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := p.Parse(t.Context(), tt.expr)
			require.NoError(t, err, "Parse(%q)", tt.expr)

			call, ok := node.(*filter.CallNode)
			require.True(t, ok, "Parse(%q) did not return CallNode, got %T", tt.expr, node)
			require.Equal(t, tt.wantOp, call.Op, "Parse(%q).Op", tt.expr)
			require.NotNil(t, call.Target, "call.Target is nil")
			require.Len(t, call.Args, 1)
		})
	}
}

func TestParser_Parse_Substring(t *testing.T) {
	p, _ := New(WithNoCache(true))

	tests := []struct {
		name     string
		expr     string
		wantArgs int
	}{
		{"one-arg", `recipient.substring(7)`, 1},
		{"two-arg", `recipient.substring(0, 4)`, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := p.Parse(t.Context(), tt.expr)
			require.NoError(t, err, "Parse(%q)", tt.expr)

			call, ok := node.(*filter.CallNode)
			require.True(t, ok, "Parse(%q) did not return CallNode, got %T", tt.expr, node)
			require.Equal(t, filter.OpSubstring, call.Op, "Parse(%q).Op", tt.expr)
			require.NotNil(t, call.Target, "call.Target is nil")
			require.Len(t, call.Args, tt.wantArgs, "Parse(%q).Args", tt.expr)
		})
	}
}

func TestParser_Parse_SizeFunction(t *testing.T) {
	p, _ := New(WithNoCache(true))

	node, err := p.Parse(t.Context(), `tags.size()`)
	require.NoError(t, err)

	call, ok := node.(*filter.CallNode)
	require.True(t, ok, "Parse() did not return CallNode, got %T", node)
	require.Equal(t, filter.OpSize, call.Op)
	require.NotNil(t, call.Target, "call.Target is nil")
}

// TestParser_Parse_SizeCallForms pins that the global spelling `size(f)`
// parses to the same node as the receiver spelling `f.size()`: the operand
// in Target, no arguments. Every consumer relies on that single shape — the
// Evaluator used to dereference the nil Target the global form left behind.
func TestParser_Parse_SizeCallForms(t *testing.T) {
	t.Parallel()

	p, err := New(WithNoCache(true))
	require.NoError(t, err)

	method, err := p.Parse(t.Context(), `tags.size() > 1`)
	require.NoError(t, err)
	global, err := p.Parse(t.Context(), `size(tags) > 1`)
	require.NoError(t, err)
	require.Equal(t, method, global)

	call, ok := global.(*filter.BinaryOpNode).Left.(*filter.CallNode)
	require.True(t, ok)
	require.Equal(t, &filter.IdentNode{Name: "tags"}, call.Target)
	require.Empty(t, call.Args)

	for _, expr := range []string{`size()`, `size(tags, name)`, `tags.size(name)`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			_, err := p.Parse(t.Context(), expr)
			require.ErrorIs(t, err, filter.ErrInvalidExpression)
		})
	}
}

func TestParser_Parse_HasMacro(t *testing.T) {
	p, _ := New(WithNoCache(true))

	node, err := p.Parse(t.Context(), `has(user.email)`)
	require.NoError(t, err)

	call, ok := node.(*filter.CallNode)
	require.True(t, ok, "Parse() did not return CallNode, got %T", node)
	require.Equal(t, filter.OpHas, call.Op)
}

func TestParser_Parse_Timestamp(t *testing.T) {
	p, _ := New(WithNoCache(true))

	node, err := p.Parse(t.Context(), `timestamp("2024-01-15T10:30:00Z")`)
	require.NoError(t, err)

	lit, ok := node.(*filter.LiteralNode)
	require.True(t, ok, "Parse() did not return LiteralNode, got %T", node)
	ts, ok := lit.Value.(time.Time)
	require.True(t, ok, "Value is not time.Time, got %T", lit.Value)

	expected := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	require.True(t, ts.Equal(expected), "timestamp = %v, want %v", ts, expected)
}

func TestParser_Parse_ComplexExpressions(t *testing.T) {
	p, _ := New(WithNoCache(true))

	tests := []struct {
		name string
		expr string
	}{
		{"compound and", `name == "John" && age >= 18 && active == true`},
		{"compound or", `status == "active" || status == "pending" || status == "new"`},
		{"mixed", `(name == "John" || name == "Jane") && age >= 18`},
		{"nested field comparison", `address.city == "NYC" && address.zip == "10001"`},
		{"in with and", `status in ["active", "pending"] && age >= 18`},
		{"string func with and", `name.startsWith("J") && active == true`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := p.Parse(t.Context(), tt.expr)
			require.NoError(t, err, "Parse(%q)", tt.expr)
			require.NotNil(t, node, "Parse() returned nil node")
		})
	}
}

func TestParser_Parse_InvalidExpressions(t *testing.T) {
	p, _ := New(WithNoCache(true))

	tests := []struct {
		name string
		expr string
	}{
		{"unclosed string", `name == "John`},
		{"unclosed paren", `(name == "John"`},
		{"invalid operator", `name === "John"`},
		{"missing operand", `name ==`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.Parse(t.Context(), tt.expr)
			require.Error(t, err, "Parse(%q) expected error", tt.expr)
		})
	}
}

func TestParser_MustParse_Panic(t *testing.T) {
	p, _ := New(WithNoCache(true))

	require.Panics(t, func() {
		p.MustParse(`invalid ===`)
	}, "MustParse() did not panic on invalid expression")
}

func TestParser_MustParse_Success(t *testing.T) {
	p, _ := New(WithNoCache(true))

	node := p.MustParse(`name == "John"`)
	require.NotNil(t, node, "MustParse() returned nil")
}

func TestParser_Cache(t *testing.T) {
	p, _ := New()

	expr := `name == "John"`

	node1, err := p.Parse(t.Context(), expr)
	require.NoError(t, err, "first Parse()")

	node2, err := p.Parse(t.Context(), expr)
	require.NoError(t, err, "second Parse()")

	require.Equal(t, node1, node2, "cached parses differ")
	require.NotSame(t, node1, node2, "cached AST must be caller-owned")
}

func TestNodeKind_String(t *testing.T) {
	tests := []struct {
		kind filter.NodeKind
		want string
	}{
		{filter.NodeKindLiteral, "NodeKindLiteral"},
		{filter.NodeKindIdent, "NodeKindIdent"},
		{filter.NodeKindBinaryOp, "NodeKindBinaryOp"},
		{filter.NodeKindUnaryOp, "NodeKindUnaryOp"},
		{filter.NodeKindCall, "NodeKindCall"},
		{filter.NodeKindList, "NodeKindList"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			require.Equal(t, tt.want, tt.kind.String())
		})
	}

	// Test unknown kind returns formatted string
	unknown := filter.NodeKind(99)
	require.True(t, strings.HasPrefix(unknown.String(), "NodeKind("), "Unknown NodeKind.String() = %q", unknown.String())
}

func TestOperator_String(t *testing.T) {
	tests := []struct {
		op   filter.Operator
		want string
	}{
		{filter.OpEqual, "OpEqual"},
		{filter.OpNotEqual, "OpNotEqual"},
		{filter.OpLT, "OpLT"},
		{filter.OpLTE, "OpLTE"},
		{filter.OpGT, "OpGT"},
		{filter.OpGTE, "OpGTE"},
		{filter.OpAnd, "OpAnd"},
		{filter.OpOr, "OpOr"},
		{filter.OpNot, "OpNot"},
		{filter.OpIn, "OpIn"},
		{filter.OpContains, "OpContains"},
		{filter.OpStartsWith, "OpStartsWith"},
		{filter.OpEndsWith, "OpEndsWith"},
		{filter.OpMatches, "OpMatches"},
		{filter.OpSize, "OpSize"},
		{filter.OpHas, "OpHas"},
		{filter.OpExists, "OpExists"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			require.Equal(t, tt.want, tt.op.String())
		})
	}

	// Test unknown operator returns formatted string
	unknown := filter.Operator(99)
	require.True(t, strings.HasPrefix(unknown.String(), "Operator("), "Unknown Operator.String() = %q", unknown.String())
}

func TestOperator_IsComparison(t *testing.T) {
	comparisons := []filter.Operator{filter.OpEqual, filter.OpNotEqual, filter.OpLT, filter.OpLTE, filter.OpGT, filter.OpGTE}
	for _, op := range comparisons {
		require.True(t, op.IsComparison(), "%v.IsComparison()", op)
	}

	nonComparisons := []filter.Operator{filter.OpAnd, filter.OpOr, filter.OpNot, filter.OpIn, filter.OpContains}
	for _, op := range nonComparisons {
		require.False(t, op.IsComparison(), "%v.IsComparison()", op)
	}
}

func TestOperator_IsLogical(t *testing.T) {
	logical := []filter.Operator{filter.OpAnd, filter.OpOr, filter.OpNot}
	for _, op := range logical {
		require.True(t, op.IsLogical(), "%v.IsLogical()", op)
	}

	nonLogical := []filter.Operator{filter.OpEqual, filter.OpIn, filter.OpContains}
	for _, op := range nonLogical {
		require.False(t, op.IsLogical(), "%v.IsLogical()", op)
	}
}

func TestOperator_IsStringOp(t *testing.T) {
	stringOps := []filter.Operator{filter.OpContains, filter.OpStartsWith, filter.OpEndsWith, filter.OpMatches}
	for _, op := range stringOps {
		require.True(t, op.IsStringOp(), "%v.IsStringOp()", op)
	}

	nonStringOps := []filter.Operator{filter.OpEqual, filter.OpAnd, filter.OpIn, filter.OpSize, filter.OpSubstring}
	for _, op := range nonStringOps {
		require.False(t, op.IsStringOp(), "%v.IsStringOp()", op)
	}
}

func TestWalk(t *testing.T) {
	p, _ := New(WithNoCache(true))
	node, _ := p.Parse(t.Context(), `name == "John" && age >= 18`)

	count := 0

	filter.Walk(node, func(n filter.Node) bool {
		count++
		return true
	})

	require.NotZero(t, count, "Walk did not visit any nodes")
}

func TestAllNodes(t *testing.T) {
	p, _ := New(WithNoCache(true))
	node, _ := p.Parse(t.Context(), `name == "John"`)

	count := 0
	for range filter.AllNodes(node) {
		count++
	}

	require.Equal(t, filter.CountNodes(node), count)
}

func TestDepth(t *testing.T) {
	tests := []struct {
		expr     string
		minDepth int
	}{
		{`"hello"`, 1},
		{`name == "John"`, 2},
		{`name == "John" && age >= 18`, 3},
	}

	p, _ := New(WithNoCache(true))

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			node, _ := p.Parse(t.Context(), tt.expr)
			d := filter.Depth(node)
			require.GreaterOrEqual(t, d, tt.minDepth, "Depth(%q)", tt.expr)
		})
	}

	// Test nil
	require.Equal(t, 0, filter.Depth(nil), "Depth(nil) should be 0")
}

func TestParser_Parse_ExpressionTooLong(t *testing.T) {
	p, _ := New(WithNoCache(true), WithMaxExpressionLength(50))

	t.Run("expression within limit", func(t *testing.T) {
		_, err := p.Parse(t.Context(), `name == "John"`)
		require.NoError(t, err)
	})

	t.Run("expression exceeding limit", func(t *testing.T) {
		long := `name == "` + strings.Repeat("a", 50) + `"`
		_, err := p.Parse(t.Context(), long)
		require.ErrorIs(t, err, filter.ErrExpressionTooLong)
	})

	t.Run("default limit allows reasonable expressions", func(t *testing.T) {
		pDefault, _ := New(WithNoCache(true))
		_, err := pDefault.Parse(t.Context(), `name == "John" && age >= 18`)
		require.NoError(t, err)
	})
}

func TestValidateRegex(t *testing.T) {
	t.Run("valid short regex", func(t *testing.T) {
		require.NoError(t, filter.ValidateRegex("^hello.*", 1024))
	})

	t.Run("regex exceeding length", func(t *testing.T) {
		long := strings.Repeat("a", 1025)
		require.ErrorIs(t, filter.ValidateRegex(long, 1024), regexanchor.ErrInvalidRegex)
	})

	t.Run("invalid regex pattern", func(t *testing.T) {
		require.ErrorIs(t, filter.ValidateRegex("[invalid", 1024), regexanchor.ErrInvalidRegex)
	})
}

func TestParser_Parse_DefaultExpressionLength(t *testing.T) {
	p, _ := New(WithNoCache(true))

	// Build an expression that exceeds DefaultMaxExpressionLength (4096)
	long := `name == "` + strings.Repeat("x", DefaultMaxExpressionLength) + `"`
	_, err := p.Parse(t.Context(), long)
	require.ErrorIs(t, err, filter.ErrExpressionTooLong)
}

func TestNode_Children(t *testing.T) {
	p, _ := New(WithNoCache(true))

	t.Run("LiteralNode has no children", func(t *testing.T) {
		node, _ := p.Parse(t.Context(), `"hello"`)

		count := 0
		for range node.Children() {
			count++
		}

		require.Equal(t, 0, count, "LiteralNode children")
	})

	t.Run("BinaryOpNode has 2 children", func(t *testing.T) {
		node, _ := p.Parse(t.Context(), `name == "John"`)

		count := 0
		for range node.Children() {
			count++
		}

		require.Equal(t, 2, count, "BinaryOpNode children")
	})

	t.Run("ListNode has children for each element", func(t *testing.T) {
		node, _ := p.Parse(t.Context(), `status in ["a", "b", "c"]`)
		binOp := node.(*filter.BinaryOpNode)
		list := binOp.Right.(*filter.ListNode)

		count := 0
		for range list.Children() {
			count++
		}

		require.Equal(t, 3, count, "ListNode children")
	})
}

func TestParser_Parse_CustomFunction_CompareField(t *testing.T) {
	p, err := New(
		WithNoCache(true),
		WithCustomFunctions(map[string]filter.CustomFunction{
			"createdAfter": filterfuncs.CompareField("createdAt", filter.OpGT),
		}),
	)
	require.NoError(t, err)

	node, err := p.Parse(t.Context(), `createdAfter("2024-01-01")`)
	require.NoError(t, err)

	binOp, ok := node.(*filter.BinaryOpNode)
	require.True(t, ok, "expected BinaryOpNode, got %T", node)
	require.Equal(t, filter.OpGT, binOp.Op)

	ident, ok := binOp.Left.(*filter.IdentNode)
	require.True(t, ok, "Left is not IdentNode, got %T", binOp.Left)
	require.Equal(t, "createdAt", ident.Name)

	lit, ok := binOp.Right.(*filter.LiteralNode)
	require.True(t, ok, "Right is not LiteralNode, got %T", binOp.Right)
	require.Equal(t, "2024-01-01", lit.Value)
}

func TestParser_Parse_CustomFunction_HandlerError(t *testing.T) {
	p, err := New(
		WithNoCache(true),
		WithCustomFunctions(map[string]filter.CustomFunction{
			"alwaysFails": func([]filter.Node) (filter.Node, error) {
				return nil, errors.New("nope")
			},
		}),
	)
	require.NoError(t, err)

	_, err = p.Parse(t.Context(), `alwaysFails(1)`)
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
	require.Contains(t, err.Error(), "alwaysFails")
}

func TestParser_Parse_CustomFunction_NilNode(t *testing.T) {
	p, err := New(
		WithNoCache(true),
		WithCustomFunctions(map[string]filter.CustomFunction{
			"returnsNil": func([]filter.Node) (filter.Node, error) { return nil, nil },
		}),
	)
	require.NoError(t, err)

	_, err = p.Parse(t.Context(), `returnsNil(1)`)
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
	require.Contains(t, err.Error(), "nil node")
}

func TestParser_Parse_CustomFunction_Composition(t *testing.T) {
	p, err := New(
		WithNoCache(true),
		WithCustomFunctions(map[string]filter.CustomFunction{
			"createdAfter": filterfuncs.CompareField("createdAt", filter.OpGT),
		}),
	)
	require.NoError(t, err)

	node, err := p.Parse(t.Context(), `createdAfter("2024-01-01") && status == "ok"`)
	require.NoError(t, err)

	andNode, ok := node.(*filter.BinaryOpNode)
	require.True(t, ok, "expected BinaryOpNode, got %T", node)
	require.Equal(t, filter.OpAnd, andNode.Op)

	left, ok := andNode.Left.(*filter.BinaryOpNode)
	require.True(t, ok, "Left is not BinaryOpNode, got %T", andNode.Left)
	require.Equal(t, filter.OpGT, left.Op)
	leftIdent, ok := left.Left.(*filter.IdentNode)
	require.True(t, ok)
	require.Equal(t, "createdAt", leftIdent.Name)
}

func TestParser_Parse_CustomFunction_BetweenHandler(t *testing.T) {
	between := func(args []filter.Node) (filter.Node, error) {
		if len(args) != 3 {
			return nil, errors.New("between requires 3 args")
		}

		field, ok := args[0].(*filter.IdentNode)
		if !ok {
			return nil, errors.New("first arg must be a field identifier")
		}

		return &filter.BinaryOpNode{
			Op:    filter.OpAnd,
			Left:  &filter.BinaryOpNode{Op: filter.OpGTE, Left: field, Right: args[1]},
			Right: &filter.BinaryOpNode{Op: filter.OpLTE, Left: field, Right: args[2]},
		}, nil
	}

	p, err := New(
		WithNoCache(true),
		WithCustomFunctions(map[string]filter.CustomFunction{"between": between}),
	)
	require.NoError(t, err)

	node, err := p.Parse(t.Context(), `between(age, 18, 65)`)
	require.NoError(t, err)

	andNode, ok := node.(*filter.BinaryOpNode)
	require.True(t, ok, "expected BinaryOpNode, got %T", node)
	require.Equal(t, filter.OpAnd, andNode.Op)
	require.Equal(t, filter.OpGTE, andNode.Left.(*filter.BinaryOpNode).Op)
	require.Equal(t, filter.OpLTE, andNode.Right.(*filter.BinaryOpNode).Op)
}

func TestParser_Parse_CustomFunction_UnknownFallsThrough(t *testing.T) {
	p, err := New(
		WithNoCache(true),
		WithCustomFunctions(map[string]filter.CustomFunction{
			"createdAfter": filterfuncs.CompareField("createdAt", filter.OpGT),
		}),
	)
	require.NoError(t, err)

	_, err = p.Parse(t.Context(), `unknownFunc(1)`)
	require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
}

func TestNew_CustomFunctions_ReservedName(t *testing.T) {
	_, err := New(WithCustomFunctions(map[string]filter.CustomFunction{
		"contains": func([]filter.Node) (filter.Node, error) { return nil, nil },
	}))
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
	require.Contains(t, err.Error(), "contains")
}

func TestNew_CustomFunctions_NilHandler(t *testing.T) {
	_, err := New(WithCustomFunctions(map[string]filter.CustomFunction{
		"createdAfter": nil,
	}))
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
	require.Contains(t, err.Error(), "nil handler")
}

func TestCompareField_NonComparisonOperator(t *testing.T) {
	h := filterfuncs.CompareField("createdAt", filter.OpAnd)
	_, err := h([]filter.Node{&filter.LiteralNode{Value: "x"}})
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
	require.Contains(t, err.Error(), "not a comparison")
}

func TestCompareField_WrongArity(t *testing.T) {
	h := filterfuncs.CompareField("createdAt", filter.OpGT)

	_, err := h(nil)
	require.ErrorIs(t, err, filter.ErrInvalidExpression)

	_, err = h([]filter.Node{&filter.LiteralNode{Value: 1}, &filter.LiteralNode{Value: 2}})
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
}

func TestParser_Parse_AllowedFunctions(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		wantErr error
	}{
		{"allowed built-in", `name.contains("a")`, nil},
		{"blocked built-in", `name.matches("a")`, filter.ErrFunctionNotAllowed},
		{"allowed custom", `createdAfter("2024-01-01")`, nil},
		{"unregistered custom (blocked by allowlist first)", `unknownFunc(1)`, filter.ErrFunctionNotAllowed},
		{"operator: equality", `name == "John"`, nil},
		{"operator: logical and", `name == "John" && active`, nil},
		{"operator: logical or", `a == 1 || b == 2`, nil},
		{"operator: in", `status in ["a", "b"]`, nil},
		{"operator: unary not", `!active`, nil},
		{"macro: has", `has(user.email)`, nil},
		{"comparison chain", `age >= 18 && age < 65`, nil},
	}

	p, err := New(
		WithNoCache(true),
		WithAllowedFunctions("contains", "createdAfter"),
		WithCustomFunctions(map[string]filter.CustomFunction{
			"createdAfter": filterfuncs.CompareField("createdAt", filter.OpGT),
		}),
	)
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.Parse(t.Context(), tt.expr)
			if tt.wantErr == nil {
				require.NoError(t, err, "Parse(%q)", tt.expr)
				return
			}

			require.ErrorIs(t, err, tt.wantErr, "Parse(%q)", tt.expr)
		})
	}
}

func TestParser_Parse_AllowedFunctions_AllowedButUnregistered(t *testing.T) {
	p, err := New(
		WithNoCache(true),
		WithAllowedFunctions("createdAfter"),
		// no custom registration for createdAfter
	)
	require.NoError(t, err)

	_, err = p.Parse(t.Context(), `createdAfter("2024-01-01")`)
	require.ErrorIs(t, err, filter.ErrUnsupportedOperation,
		"allowlist passes but no handler exists, must surface as ErrUnsupportedOperation")
}

func TestParser_Parse_AllowedFunctions_NotConfiguredAllowsAll(t *testing.T) {
	p, err := New(WithNoCache(true))
	require.NoError(t, err)

	_, err = p.Parse(t.Context(), `name.matches("^J.*")`)
	require.NoError(t, err, "without WithAllowedFunctions every function is allowed")
}

func TestIsUserFunction(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"_==_", false},
		{"_!=_", false},
		{"_<_", false},
		{"_<=_", false},
		{"_>_", false},
		{"_>=_", false},
		{"_&&_", false},
		{"_||_", false},
		{"!_", false},
		{"@in", false},
		{"has", false},
		{"contains", true},
		{"startsWith", true},
		{"endsWith", true},
		{"matches", true},
		{"size", true},
		{"timestamp", true},
		{"createdAfter", true},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isUserFunction(tt.name))
		})
	}
}
