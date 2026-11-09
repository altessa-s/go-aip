// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package functions

import (
	filter "github.com/altessa-s/go-aip/filter"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// CEL function names exposed by [SoftDeleteFilters].
const (
	SoftDeleteFuncNotDeleted  = "notDeleted"
	SoftDeleteFuncOnlyDeleted = "onlyDeleted"
)

// SoftDeleteFilters returns predicates for the canonical soft-delete
// pattern that pivots on the [TimestampFieldDeletedAt] field:
//
//	notDeleted()  → deletedAt == null
//	onlyDeleted() → deletedAt != null
//
// Equality with null matches both the "field is null" and "field is
// missing" cases under MongoDB and the in-memory evaluator, which is
// the semantics callers usually want for soft delete. If your storage
// distinguishes the two, gate explicitly with `has(deletedAt)` instead.
//
// The functions are not registered automatically — opt in explicitly
// via [github.com/altessa-s/go-aip/filter/parser.WithCustomFunctions]
// or [github.com/altessa-s/go-aip/filter/parser.WithCustomFunctions]:
//
//	filterparser.WithCustomFunctions(functions.SoftDeleteFilters())
//
// Each call returns a fresh map; callers can drop entries before
// passing it on.
func SoftDeleteFilters() map[string]filter.CustomFunction {
	return map[string]filter.CustomFunction{
		SoftDeleteFuncNotDeleted:  softDeleteNullCheck(filter.OpEqual, SoftDeleteFuncNotDeleted),
		SoftDeleteFuncOnlyDeleted: softDeleteNullCheck(filter.OpNotEqual, SoftDeleteFuncOnlyDeleted),
	}
}

// softDeleteNullCheck builds a no-arg handler that compares
// deletedAt against nil with the given operator.
func softDeleteNullCheck(op filter.Operator, name string) filter.CustomFunction {
	return func(args []filter.Node) (filter.Node, error) {
		if len(args) != 0 {
			return nil, coreerrs.Wrapf(filter.ErrInvalidExpression,
				"%s expects 0 arguments, got %d", name, len(args))
		}

		return &filter.BinaryOpNode{
			Op:    op,
			Left:  &filter.IdentNode{Name: TimestampFieldDeletedAt},
			Right: &filter.LiteralNode{Value: nil},
		}, nil
	}
}
