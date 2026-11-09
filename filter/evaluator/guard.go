// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Guard counts the operations and the nesting depth of one evaluation against the limits of a policy ([filter.WithMaxOperations],
// [filter.WithMaxDepth]), with the errors the evaluator returns: a profile implementing its own [Program] walks a filter under
// the same limits. It is the mutable state of one evaluation and not safe for concurrent use.
type Guard struct {
	ops      int
	opsLimit int
	depth    int
	maxDepth int
}

// Reset starts counting anew under maxOperations operations and maxDepth levels of nesting.
func (g *Guard) Reset(maxOperations, maxDepth int) {
	*g = Guard{opsLimit: maxOperations, maxDepth: maxDepth}
}

// Op counts one operation; it fails with [filter.ErrMaxOperationsExceeded] beyond the limit.
func (g *Guard) Op() error {
	g.ops++
	if g.ops > g.opsLimit {
		return coreerrs.Wrapf(filter.ErrMaxOperationsExceeded, "operation count %d exceeds maximum %d", g.ops, g.opsLimit)
	}

	return nil
}

// Enter counts the operation of a compound node and enters it, one level deeper; it fails with
// [filter.ErrMaxOperationsExceeded] or [filter.ErrMaxDepthExceeded] beyond a limit. A successful Enter is undone by
// [Guard.Leave].
func (g *Guard) Enter() error {
	if err := g.Op(); err != nil {
		return err
	}

	if g.depth >= g.maxDepth {
		return coreerrs.Wrapf(filter.ErrMaxDepthExceeded, "depth %d exceeds maximum %d", g.depth, g.maxDepth)
	}

	g.depth++

	return nil
}

// Leave leaves the node a successful [Guard.Enter] entered.
func (g *Guard) Leave() { g.depth-- }
