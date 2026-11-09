// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/filter/parser"
)

type loweredPredicate struct{ filter.Node }

func (n loweredPredicate) Lower() (filter.Node, error) { return n.Node, nil }

type foldingComparator struct{}

func (foldingComparator) Compare(op filter.Operator, left, right any) (bool, bool, error) {
	a, aok := left.(string)

	b, bok := right.(string)
	if op != filter.OpEqual || !aok || !bok {
		return false, false, nil
	}

	return strings.EqualFold(a, b), true, nil
}

func TestExtensionPipeline(t *testing.T) {
	t.Parallel()

	p, err := parser.New(parser.WithCustomFunctions(map[string]filter.CustomFunction{
		"active": func([]filter.Node) (filter.Node, error) {
			return loweredPredicate{Node: &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "state"}, Right: &filter.LiteralNode{Value: "active"}}}, nil
		},
	}))
	require.NoError(t, err)
	n, err := p.Parse(t.Context(), "active()")
	require.NoError(t, err)
	_, err = filter.Canonical(n)
	require.NoError(t, err)
	e, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAllowedFields("state")), evaluator.WithComparator(foldingComparator{}))
	require.NoError(t, err)
	match, err := e.Evaluate(n, map[string]any{"state": "ACTIVE"})
	require.NoError(t, err)
	require.True(t, match)
}
