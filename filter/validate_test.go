// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		expression string
		portable   bool
		wantErr    error
	}{
		{name: "comparison", expression: `age >= 18`},
		{name: "bare bool accepted by portable contract", expression: `active`, portable: true},
		{name: "bare string rejected by portable contract", expression: `name`, portable: true, wantErr: filter.ErrFieldTypeMismatch},
		{name: "bare array rejected by portable contract", expression: `flags`, portable: true, wantErr: filter.ErrFieldTypeMismatch},
		{name: "bare string remains accepted by default", expression: `name`},
		{name: "portable regex", expression: `name.matches("^a.*$")`, portable: true},
		{name: "null in list allowed by default", expression: `state in [null, "active"]`},
		{name: "ordered null allowed by default", expression: `age < null`},
		{name: "empty in allowed by default", expression: `state in []`},
		{name: "nonportable regex allowed by default", expression: `name.matches("(?i)abc")`},
		{name: "null in list rejected", expression: `state in [null, "active"]`, portable: true, wantErr: filter.ErrInvalidExpression},
		{name: "less-than null rejected", expression: `age < null`, portable: true, wantErr: filter.ErrInvalidExpression},
		{name: "less-than-or-equal null rejected", expression: `age <= null`, portable: true, wantErr: filter.ErrInvalidExpression},
		{name: "greater-than null rejected", expression: `age > null`, portable: true, wantErr: filter.ErrInvalidExpression},
		{name: "greater-than-or-equal null rejected", expression: `age >= null`, portable: true, wantErr: filter.ErrInvalidExpression},
		{name: "size ordered null rejected", expression: `name.size() < null`, portable: true, wantErr: filter.ErrInvalidExpression},
		{name: "empty in rejected", expression: `state in []`, portable: true, wantErr: filter.ErrInvalidExpression},
		{name: "leading ASCII case flag accepted", expression: `name.matches("(?i)abc")`, portable: true},
		{name: "case flag on non-ASCII rejected", expression: `name.matches("(?i)é")`, portable: true, wantErr: filter.ErrUnsupportedOperation},
		{name: "inner flag rejected", expression: `name.matches("a(?i)b")`, portable: true, wantErr: filter.ErrUnsupportedOperation},
		{name: "other flag rejected", expression: `name.matches("(?s)abc")`, portable: true, wantErr: filter.ErrUnsupportedOperation},
		{name: "alternation rejected", expression: `name.matches("a|b")`, portable: true, wantErr: filter.ErrUnsupportedOperation},
		{name: "unescaped regex metacharacter rejected", expression: `name.matches("a.b")`, portable: true, wantErr: filter.ErrUnsupportedOperation},
		{name: "escaped literal accepted", expression: `name.matches("a\\.b")`, portable: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := []filter.PolicyOption{filter.WithAllowedFields("age", "state", "name", "active", "flags"),
				filter.WithFieldTypes(map[string]filter.FieldKind{"active": filter.FieldKindBool, "name": filter.FieldKindString, "flags": filter.FieldKindBool}),
				filter.WithArrayFields("flags")}
			if tt.portable {
				opts = append(opts, filter.WithPortableContract(true))
			}

			policy, err := filter.NewPolicy(opts...)
			require.NoError(t, err)

			err = filter.Validate(filtertest.MustParseFilter(t, tt.expression), policy)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestPortableValidationRejectsNonFiniteFloatOperands(t *testing.T) {
	t.Parallel()

	policy, err := filter.NewPolicy(filter.WithAllowedFields("score"), filter.WithFieldTypes(map[string]filter.FieldKind{"score": filter.FieldKindFloat}), filter.WithPortableContract(true))
	require.NoError(t, err)

	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		node := &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "score"}, Right: &filter.LiteralNode{Value: value}}
		require.ErrorIs(t, filter.Validate(node, policy), filter.ErrInvalidExpression)
	}
}
