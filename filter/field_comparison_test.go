// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// TestEvaluator_FieldLiteralComparison pins `field op literal`, the shape the default evaluator compares most: the field's options
// (allow-list, mapping, declared kind and enum values), a dotted or mapped path, an absent field, the literal on either side, and
// the order in which the checks, the operation limit and the data budget fail.
func TestEvaluator_FieldLiteralComparison(t *testing.T) {
	t.Parallel()

	data := map[string]any{
		"name": "Alice",
		"age":  int32(30),
		"role": int64(2),
		"a":    map[string]any{"b": int64(1)},
	}

	for _, tc := range []struct {
		name string
		expr string
		opts []any
		want bool
		err  error
	}{
		{name: "scalar equal", expr: `name == "Alice"`, want: true},
		{name: "scalar ordering", expr: `name < "Bob"`, want: true},
		{name: "typed data value", expr: `age == 30`, want: true},
		{name: "literal on the left", expr: `"Alice" == name`, want: true},
		{name: "dotted path", expr: `a.b == 1`, want: true},
		{name: "mapped field", expr: `n == "Alice"`, opts: []any{filter.WithFieldMapping(map[string]string{"n": "name"})}, want: true},
		{name: "mapped to dotted path", expr: `x >= 1`, opts: []any{filter.WithFieldMapping(map[string]string{"x": "a.b"})}, want: true},
		{name: "absent equals null", expr: `missing == null`, want: true},
		{name: "absent differs from a value", expr: `missing == "x"`, want: false},
		{name: "absent is not ordered", expr: `missing < 1`, want: false}, // unknown, as in SQL: no match
		{name: "absent is not ordered under negation", expr: `!(missing < 1)`, want: false},
		{name: "unknown absorbed by or", expr: `missing < 1 || age == 30`, want: true},
		{name: "incomparable kinds", expr: `name == 1`, err: filter.ErrUnsupportedType},
		{
			name: "field outside the allow-list", expr: `age == 30`,
			opts: []any{withPolicy(filter.WithAllowedFields("name"))}, err: filter.ErrFieldNotAllowed,
		},
		{
			name: "allowed field", expr: `name == "Alice"`,
			opts: []any{withPolicy(filter.WithAllowedFields("name"))}, want: true,
		},
		{
			name: "allow-list checked before literal kind", expr: `age == "x"`,
			opts: []any{withPolicy(filter.WithAllowedFields("name")), withPolicy(filter.WithFieldTypes(map[string]filter.FieldKind{"age": filter.FieldKindInt}))},
			err:  filter.ErrFieldNotAllowed,
		},
		{
			name: "declared kind", expr: `age == 30`,
			opts: []any{withPolicy(filter.WithFieldTypes(map[string]filter.FieldKind{"age": filter.FieldKindInt}))}, want: true,
		},
		{
			name: "enum member", expr: `role == 2`,
			opts: []any{withPolicy(filter.WithEnumValues(map[string][]int64{"role": {1, 2}}))}, want: true,
		},
		{
			name: "enum non-member", expr: `role == 9`,
			opts: []any{withPolicy(filter.WithEnumValues(map[string][]int64{"role": {1, 2}}))}, err: filter.ErrEnumValueNotAllowed,
		},
		{
			name: "unsigned enum member", expr: `role == 2u`,
			opts: []any{withPolicy(filter.WithEnumValues(map[string][]int64{"role": {1, 2}}))}, want: true,
		},
		{
			name: "unsigned enum non-member", expr: `role == 9u`,
			opts: []any{withPolicy(filter.WithEnumValues(map[string][]int64{"role": {1, 2}}))}, err: filter.ErrEnumValueNotAllowed,
		},
		{
			name: "enum on the right", expr: `9 == role`,
			opts: []any{withPolicy(filter.WithEnumValues(map[string][]int64{"role": {1, 2}}))}, err: filter.ErrEnumValueNotAllowed,
		},
		{name: "operation limit", expr: `name == "Alice"`, opts: []any{withPolicy(filter.WithMaxOperations(2))}, err: filter.ErrMaxOperationsExceeded},
		{name: "operation limit met", expr: `name == "Alice"`, opts: []any{withPolicy(filter.WithMaxOperations(3))}, want: true},
		{name: "data budget", expr: `name == "Alice"`, opts: []any{filtereval.WithMaxDataBudget(1)}, err: filter.ErrDataLimitExceeded},
		{name: "data budget met", expr: `name == "Alice"`, opts: []any{filtereval.WithMaxDataBudget(2)}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			node, err := newTestParser(t).Parse(t.Context(), tc.expr)
			require.NoError(t, err)

			got, err := mustEvaluator(t, tc.opts...).Evaluate(node, data)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
