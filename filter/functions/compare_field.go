// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package functions

import (
	filter "github.com/altessa-s/go-aip/filter"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// CompareField returns a [filter.CustomFunction] that expands name(arg) into
// BinaryOpNode{op, IdentNode(field), arg}. It is the canonical building
// block for semantic shortcuts such as createdAfter or updatedBefore:
//
//	filterparser.WithCustomFunctions(map[string]filter.CustomFunction{
//	    "createdAfter":  functions.CompareField("createdAt", filter.OpGT),
//	    "updatedBefore": functions.CompareField("updatedAt", filter.OpLT),
//	})
//
// op must be a comparison operator (see [filter.Operator.IsComparison]);
// otherwise the handler returns an error wrapping
// [filter.ErrInvalidExpression] when invoked.
func CompareField(field string, op filter.Operator) filter.CustomFunction {
	return func(args []filter.Node) (filter.Node, error) {
		if len(args) != 1 {
			return nil, coreerrs.Wrapf(filter.ErrInvalidExpression,
				"function expects 1 argument, got %d", len(args))
		}

		if !op.IsComparison() {
			return nil, coreerrs.Wrapf(filter.ErrInvalidExpression,
				"operator %v is not a comparison", op)
		}

		return &filter.BinaryOpNode{Op: op, Left: &filter.IdentNode{Name: field}, Right: args[0]}, nil
	}
}
