// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// TestUnknownErrorTexts pins every data-driven unknown to the text and sentinel of the coreerrs.Wrap/Wrapf error it was built
// with per record before the unknowns were shared: "<detail>: <sentinel>", found by errors.Is.
func TestUnknownErrorTexts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  error
		want error
	}{
		{"result", errUnknownResult, coreerrs.Wrap(filter.ErrInvalidExpression, "expression must evaluate to bool, got null")},
		{"not", errUnknownNotOperand, coreerrs.Wrap(filter.ErrInvalidExpression, "! requires bool operand, got null")},
		{"size", errUnknownSizeTarget, coreerrs.Wrap(filter.ErrUnsupportedOperation, "size() not supported for null")},
		{"substring target", errUnknownSubstringTarget, coreerrs.Wrap(filter.ErrInvalidExpression, "substring() target is null")},
		{"int argument", errUnknownIntArgument, coreerrs.Wrap(filter.ErrInvalidExpression, "argument is null")},
		{"and", logicalAnd.null, coreerrs.Wrapf(filter.ErrInvalidExpression, "%s requires bool operands, got null", "&&")},
		{"or", logicalOr.null, coreerrs.Wrapf(filter.ErrInvalidExpression, "%s requires bool operands, got null", "||")},
		{"string function", stringPredicate.null, coreerrs.Wrapf(filter.ErrInvalidExpression, "%s argument is null", "string function")},
		{"matches", matchesFunction.null, coreerrs.Wrapf(filter.ErrInvalidExpression, "%s argument is null", "matches()")},
		{"substring range", substringOutOfRange(-1, 7, 3), coreerrs.Wrapf(filter.ErrInvalidExpression,
			"substring(%d, %d) out of range for string of length %d", int64(-1), int64(7), 3)},
	}

	for op := range filter.Operator(255) {
		tests = append(tests, struct {
			name string
			got  error
			want error
		}{op.String(), nullComparisonUnknown(op), coreerrs.Wrapf(filter.ErrUnsupportedOperation, "cannot use %v with nil", op)})
	}

	for _, tc := range tests {
		require.Equal(t, tc.want.Error(), tc.got.Error(), tc.name)

		for _, sentinel := range []error{filter.ErrInvalidExpression, filter.ErrUnsupportedOperation} {
			require.Equal(t, errors.Is(tc.want, sentinel), errors.Is(tc.got, sentinel), "%s: errors.Is(%v)", tc.name, sentinel)
		}

		_, ok := errors.AsType[*unknownError](tc.got)
		require.True(t, ok, tc.name)
	}
}
