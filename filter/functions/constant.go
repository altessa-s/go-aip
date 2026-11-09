// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package functions

import (
	filter "github.com/altessa-s/go-aip/filter"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Constant returns a [filter.CustomFunction] that takes no arguments and
// expands a CEL call name() into BinaryOpNode{op, IdentNode(field),
// LiteralNode(value)}. It is the building block for stable, parameter
// less predicates over a domain enum or a fixed threshold:
//
//	filterparser.WithCustomFunctions(map[string]filter.CustomFunction{
//	    "isActive":     functions.Constant("status",   filter.OpEqual, "active"),
//	    "isPending":    functions.Constant("status",   filter.OpEqual, "pending"),
//	    "hasFailures":  functions.Constant("failures", filter.OpGT,    int64(0)),
//	})
//
// Pass [nil] as value to express explicit null comparisons (the same
// shape that powers [SoftDeleteFilters]). op must be a comparison
// operator (see [filter.Operator.IsComparison]); otherwise the handler
// returns an error wrapping [filter.ErrInvalidExpression] when invoked.
//
// Calling the resulting handler with any arguments is also rejected,
// since by construction the predicate is parameter-less.
func Constant(field string, op filter.Operator, value any) filter.CustomFunction {
	return func(args []filter.Node) (filter.Node, error) {
		if len(args) != 0 {
			return nil, coreerrs.Wrapf(filter.ErrInvalidExpression,
				"constant predicate expects 0 arguments, got %d", len(args))
		}

		if !op.IsComparison() {
			return nil, coreerrs.Wrapf(filter.ErrInvalidExpression,
				"operator %v is not a comparison", op)
		}

		return &filter.BinaryOpNode{
			Op:    op,
			Left:  &filter.IdentNode{Name: field},
			Right: &filter.LiteralNode{Value: value},
		}, nil
	}
}
