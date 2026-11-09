// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package functions

import (
	filter "github.com/altessa-s/go-aip/filter"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// BetweenFunc is the CEL function name registered by [BetweenFilter].
const BetweenFunc = "between"

// Between returns a [filter.CustomFunction] that expands a CEL call
//
//	between(field, lo, hi)
//
// into the canonical AST shape `field >= lo && field <= hi`. The first
// argument must be a field identifier; the remaining two are passed
// through as the bounds and may be any expression the surrounding
// translator accepts (literals, timestamps, dotted field paths).
//
// Backend translators see a regular And-tree of comparisons, so the
// helper works uniformly across mongo, redis, and lua.
func Between() filter.CustomFunction {
	return func(args []filter.Node) (filter.Node, error) {
		const wantArgs = 3
		if len(args) != wantArgs {
			return nil, coreerrs.Wrapf(filter.ErrInvalidExpression,
				"between expects %d arguments (field, lo, hi), got %d", wantArgs, len(args))
		}

		field, ok := args[0].(*filter.IdentNode)
		if !ok {
			return nil, coreerrs.Wrapf(filter.ErrInvalidExpression,
				"between: first argument must be a field identifier, got %T", args[0])
		}

		return &filter.BinaryOpNode{
			Op:    filter.OpAnd,
			Left:  &filter.BinaryOpNode{Op: filter.OpGTE, Left: field, Right: args[1]},
			Right: &filter.BinaryOpNode{Op: filter.OpLTE, Left: field, Right: args[2]},
		}, nil
	}
}

// BetweenFilter returns the [Between] handler as a one-entry map
// suitable for [github.com/altessa-s/go-aip/filter/parser.WithCustomFunctions]
// or [github.com/altessa-s/go-aip/filter/parser.WithCustomFunctions]:
//
//	filterparser.WithCustomFunctions(functions.BetweenFilter())
//
// Each call returns a fresh map.
func BetweenFilter() map[string]filter.CustomFunction {
	return map[string]filter.CustomFunction{BetweenFunc: Between()}
}
