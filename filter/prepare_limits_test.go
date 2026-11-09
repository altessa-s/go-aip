// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
)

// TestEvaluator_PrepareChecksLimitsBeforeCopying: review finding R17-1 — Prepare copied a caller-built AST before checking its
// operation and depth limits, so a chain of a million negations or a list of a million literals was copied in full (and a deep
// enough chain could exhaust the stack) before the copy failed validation. The limits are checked first: such a node fails
// without being copied. The node is built before the measurement; only Prepare is measured.
func TestEvaluator_PrepareChecksLimitsBeforeCopying(t *testing.T) { //nolint:paralleltest // measures allocations
	const size = 1_000_000

	ident := &filter.IdentNode{Name: "v"}

	var deep filter.Node = ident
	for range size {
		deep = &filter.UnaryOpNode{Op: filter.OpNot, Operand: deep}
	}

	elements := make([]filter.Node, size)
	for i := range elements {
		elements[i] = &filter.LiteralNode{Value: int64(i)}
	}

	wide := &filter.BinaryOpNode{Op: filter.OpIn, Left: ident, Right: &filter.ListNode{Elements: elements}}

	tests := []struct {
		name string
		node filter.Node
		want error
	}{
		{name: "deep", node: deep, want: filter.ErrMaxDepthExceeded},
		{name: "wide", node: wide, want: filter.ErrMaxOperationsExceeded},
	}
	for mode, eval := range evaluators(t) {
		for _, tt := range tests {
			var err error

			elapsed, allocated := measured(func() { _, err = eval.Prepare(tt.node) })

			require.ErrorIs(t, err, tt.want, "%s %s", mode, tt.name)
			requireQuick(t, elapsed, quickEvaluation, "%s %s", mode, tt.name)
			require.Less(t, allocated, uint64(smallAllocation), "%s %s", mode, tt.name)
		}
	}
}
