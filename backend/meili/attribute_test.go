// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/meili"
	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
	"github.com/altessa-s/go-aip/projection"
	"github.com/altessa-s/go-aip/projection/projectiontest"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

// unsafeAttributes are storage names that would not stay one attribute if written unquoted into a Meilisearch filter, sort
// entry or attribute list.
var unsafeAttributes = []string{
	"",
	"zz NOT EXISTS AND secret",
	"y NOT EXISTS OR secret",
	"a..b",
	".a",
	"a.",
	"a b",
	"a\tb",
	"a\nb",
	"a\x00b",
	`a"b`,
	"a'b",
	"a`b",
	"a=b",
	"a!=b",
	"a<b",
	"a>b",
	"a(b",
	"a)b",
	"a[b",
	"a,b",
	"a:b",
	"a*",
	"*",
	`a\b`,
	"ä",
}

// TestFilterTranslatorRejectsUnsafeStorageNames pins the defense in depth of SEC-1: a storage name the policy lets through but
// that is not a plain attribute fails before it reaches the filter, whatever the core field checks accept.
func TestFilterTranslatorRejectsUnsafeStorageNames(t *testing.T) {
	t.Parallel()

	names := append(unsafeAttributes, "AND", "OR", "NOT", "IN", "TO", "EXISTS", "IS", "NULL", "EMPTY", "CONTAINS", "STARTS",
		"WITH", "_geo", "_geoRadius", "_geoBoundingBox", "_geoPolygon", "_foreign", "_geoPoint", "_geoPointX", "_geoDistance")

	exprs := []string{`x == "guess"`, `x`, `!x`, `x > 1`, `x in ["a"]`, `x == null`, `x != ""`}

	for _, name := range names {
		for _, expr := range exprs {
			t.Run(name+"/"+expr, func(t *testing.T) {
				t.Parallel()

				policy := filtertest.MustPolicy(t, filter.WithUntrustedInput(true), filter.WithAllowedFields("x"))
				tr, err := meili.NewFilterTranslator(policy,
					meili.WithFilterOptions(filter.WithFieldMapping(map[string]string{"x": name})))
				require.NoError(t, err)

				out, err := tr.Translate(filtertest.MustParseFilter(t, expr))
				require.ErrorIs(t, err, filter.ErrInvalidExpression)
				require.Empty(t, out)
			})
		}
	}
}

// TestFilterTranslatorAcceptsAttributeNames pins the names the translator keeps writing unquoted.
func TestFilterTranslatorAcceptsAttributeNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"a", "A_b-c", "a.b.c", "_private", "0", "a1.b2", "and", "nulls", "ANDROID", "_geoX", "x.AND"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			policy := filtertest.MustPolicy(t, filter.WithUntrustedInput(true), filter.WithAllowedFields("x"))
			tr, err := meili.NewFilterTranslator(policy,
				meili.WithFilterOptions(filter.WithFieldMapping(map[string]string{"x": name})))
			require.NoError(t, err)

			out, err := tr.Translate(filtertest.MustParseFilter(t, `x == 1`))
			require.NoError(t, err)
			require.Equal(t, name+" = 1", out)
		})
	}
}

// TestFilterTranslatorRejectsBacktickBreakout replays the SEC-1 report: under a prefix allow-list a backtick segment carried
// filter syntax into the attribute name. Whether the parser or the core field check refuses it first does not matter; the
// translator never emits it.
func TestFilterTranslatorRejectsBacktickBreakout(t *testing.T) {
	t.Parallel()

	parser, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(t, err)

	policy, err := filter.NewPolicy(filter.WithUntrustedInput(true), filter.WithFieldAccess(fieldpolicy.NewAllowList("a.*")))
	require.NoError(t, err)

	tr, err := meili.NewFilterTranslator(policy)
	require.NoError(t, err)

	for _, expr := range []string{
		"a.`zz NOT EXISTS AND secret` == \"guess\"",
		"a.`x` == 1 && a.`y NOT EXISTS OR secret` != 0",
		"a.`.b` == 1",
		"a.`b.` == 1",
		"a.`b c` == 1",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			node, err := parser.Parse(t.Context(), expr)
			if err != nil {
				return // refused by the parser
			}

			out, err := tr.Translate(node)
			require.Error(t, err)
			require.Empty(t, out)
		})
	}

	// A hand-built AST skips the parser altogether.
	node := &filter.BinaryOpNode{
		Op:    filter.OpEqual,
		Left:  &filter.IdentNode{Name: "a.zz NOT EXISTS AND secret"},
		Right: &filter.LiteralNode{Value: "guess"},
	}

	out, err := tr.Translate(node)
	require.Error(t, err)
	require.Empty(t, out)
}

func TestOrderTranslatorRejectsUnsafeStorageNames(t *testing.T) {
	t.Parallel()

	for _, name := range unsafeAttributes {
		if name == "" {
			continue // an empty mapping target is refused by the orderby package itself
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tr, err := meili.NewOrderTranslator(orderbytest.MustPolicy(t, orderby.WithAllowedFields("x")),
				meili.WithOrderOptions(orderby.WithFieldMapping(map[string]string{"x": name})))
			require.NoError(t, err)

			out, err := tr.Translate(orderby.Spec{Keys: []orderby.Key{{Name: "x"}}})
			require.ErrorIs(t, err, orderby.ErrInvalidFieldPath)
			require.Nil(t, out)
		})
	}
}

func TestProjectionTranslatorRejectsUnsafeStorageNames(t *testing.T) {
	t.Parallel()

	for _, name := range unsafeAttributes {
		if name == "" {
			continue
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tr, err := meili.NewProjectionTranslator(projectiontest.MustPolicy(t, projection.WithAllowedFields("*")),
				meili.WithProjectionOptions(projection.WithFieldMapping(map[string]string{"nick": name})))
			require.NoError(t, err)

			out, err := tr.Translate(projection.Spec{Paths: []string{"nick"}})
			require.ErrorIs(t, err, projection.ErrInvalidFieldPath)
			require.Nil(t, out)
		})
	}
}
