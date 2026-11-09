// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package parser

import (
	"cel.dev/cel-go/common/operators"

	filter "github.com/altessa-s/go-aip/filter"
	coremaps "github.com/altessa-s/go-atlas-core/collections/maps"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// funcTimestamp is the CEL timestamp() conversion function.
const funcTimestamp = "timestamp"

// reservedFunctionNames lists CEL built-ins that callers must not
// override via [WithCustomFunctions]. Kept in sync with the explicit
// switch branches in [Parser.convertCall].
var reservedFunctionNames = coremaps.NewImmutableMap(map[string]struct{}{
	"contains":    {},
	"startsWith":  {},
	"endsWith":    {},
	"matches":     {},
	"size":        {},
	"has":         {},
	funcTimestamp: {},
	"substring":   {},
})

// isUserFunction reports whether name is a user-callable named
// function subject to [WithAllowedFunctions]. CEL operator function
// ids and the has() macro are baseline grammar. A leading underscore alone
// does not identify an operator: custom functions may use that prefix too.
func isUserFunction(name string) bool {
	switch name {
	case "", operators.Has, operators.Equals, operators.NotEquals,
		operators.Less, operators.LessEquals, operators.Greater, operators.GreaterEquals,
		operators.LogicalAnd, operators.LogicalOr, operators.LogicalNot, operators.In:
		return false
	}

	return true
}

// validateCustomFunctions rejects nil handlers and names that collide
// with reserved CEL built-ins. It is called from [New] so a
// misconfiguration surfaces at construction time rather than during
// parsing.
func validateCustomFunctions(funcs map[string]filter.CustomFunction, allowOverride bool) error {
	for name, h := range funcs {
		if h == nil {
			return coreerrs.Wrapf(filter.ErrInvalidExpression,
				"custom function %q: nil handler", name)
		}

		if !allowOverride && reservedFunctionNames.Contains(name) {
			return coreerrs.Wrapf(filter.ErrInvalidExpression,
				"custom function %q collides with built-in", name)
		}
	}

	return nil
}
