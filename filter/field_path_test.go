// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

// TestCheckFieldReference_FieldPathGrammar pins the identifier-only field grammar: a name that a CEL backtick escape, a
// root-scoped identifier or a hand-built AST can produce, but that is not a dotted path of plain identifiers, is refused
// wherever a field reference is checked.
func TestCheckFieldReference_FieldPathGrammar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		field string
		ok    bool
	}{
		{name: "identifier", field: "name", ok: true},
		{name: "underscore and digits", field: "_a1.b_2", ok: true},
		{name: "nested", field: "a.b.c", ok: true},
		{name: "empty", field: ""},
		{name: "leading dot", field: ".a"},
		{name: "trailing dot", field: "a."},
		{name: "empty segment", field: "a..b"},
		{name: "space", field: "a.b c"},
		{name: "meili breakout", field: "a.zz NOT EXISTS AND secret"},
		{name: "hyphen", field: "a.x-y"},
		{name: "slash", field: "a/b"},
		{name: "leading digit", field: "a.1b"},
		{name: "quote", field: `a"b`},
		{name: "non-ASCII", field: "имя"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.ok, filter.IsFieldPath(tt.field))

			for _, ref := range []filter.Node{
				&filter.IdentNode{Name: tt.field},
				&filter.CallNode{Op: filter.OpSize, Target: &filter.IdentNode{Name: tt.field}},
			} {
				err := filter.CheckFieldReference(ref)
				if tt.ok {
					require.NoError(t, err)
					continue
				}

				require.ErrorIs(t, err, filter.ErrInvalidExpression)
			}
		})
	}
}

// TestValidate_PrefixAllowListRefusesNonIdentifierFields reproduces SEC-1: under a wildcard allow-list ("a.*") a field
// with a non-identifier segment — whether parsed from a backtick escape or built by hand — must not pass validation,
// since some translators write the field name into their output unquoted.
func TestValidate_PrefixAllowListRefusesNonIdentifierFields(t *testing.T) {
	t.Parallel()

	policy := filtertest.MustPolicy(t, filter.WithUntrustedInput(true),
		filter.WithFieldAccess(fieldpolicy.NewAllowList("a.*")))
	parser, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(t, err)

	t.Run("plain nested field is allowed", func(t *testing.T) {
		t.Parallel()

		node, err := parser.Parse(t.Context(), `a.b == "x"`)
		require.NoError(t, err)
		require.NoError(t, filter.Validate(node, policy))
	})

	for _, expr := range []string{
		"a.`zz NOT EXISTS AND secret` == \"guess\"",
		"a.`x` == 1 && a.`y NOT EXISTS OR secret` != 0",
		"a.`.b` == 1",
		"a.`b.` == 1",
		"a.`b.c` == 1",
		"has(a.`b c`)",
		"size(a.`x-y`) > 1",
		".a.b == 1",
	} {
		t.Run("parser refuses "+expr, func(t *testing.T) {
			t.Parallel()

			_, err := parser.Parse(t.Context(), expr)
			require.ErrorIs(t, err, filter.ErrInvalidExpression)
		})
	}

	for _, field := range []string{"a.zz NOT EXISTS AND secret", "a..b", "a.", "a.b c"} {
		t.Run("validation refuses hand-built "+field, func(t *testing.T) {
			t.Parallel()

			for _, node := range []filter.Node{
				&filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: field}, Right: &filter.LiteralNode{Value: "x"}},
				&filter.CallNode{Op: filter.OpHas, Target: &filter.IdentNode{Name: field}},
			} {
				require.ErrorIs(t, filter.Validate(node, policy), filter.ErrInvalidExpression)
				require.Error(t, policy.CheckFields(node))
			}
		})
	}
}

// TestParse_EscapedPlainIdentifier keeps a backtick escape whose content is already a plain identifier: it names the same
// field as the unescaped spelling.
func TestParse_EscapedPlainIdentifier(t *testing.T) {
	t.Parallel()

	parser, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(t, err)

	node, err := parser.Parse(t.Context(), "a.`b` == 1")
	require.NoError(t, err)
	require.Equal(t, "a.b", node.(*filter.BinaryOpNode).Left.(*filter.IdentNode).Name) //nolint:forcetypeassert // shape asserted by the parse
}
