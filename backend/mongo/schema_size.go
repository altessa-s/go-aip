// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"fmt"
	"math"
	"reflect"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// MaxSizeOperand is the largest size() operand: MongoDB takes the operand of $size as a 32-bit integer.
const MaxSizeOperand = math.MaxInt32

// CheckSizeOperand validates the operand a size() of field (its CEL-side name) is compared with: a number in 0..[MaxSizeOperand]
// (NaN excluded). Anything else fails with a [*coreerrs.FieldError] naming field, which matches [coreerrs.ErrInvalidArgument]
// and [filter.ErrInvalidExpression] — MongoDB rejects a negative or larger $size operand at query time, and no size compares with a
// non-number. A fractional operand is valid: no size equals it and every size differs from it. The MongoDB translator and an
// evaluator.Evaluator with evaluator.WithProfile call it, so both refuse the same comparisons.
func CheckSizeOperand(field string, value any) error {
	var ok bool

	switch v := value.(type) {
	case int64:
		ok = v >= 0 && v <= MaxSizeOperand
	case int:
		ok = v >= 0 && v <= MaxSizeOperand
	case int32:
		ok = v >= 0
	case uint64:
		ok = v <= MaxSizeOperand
	case float64:
		ok = v >= 0 && v <= MaxSizeOperand // false for NaN
	}

	if !ok {
		return &coreerrs.FieldError{
			Field:  field,
			Reason: fmt.Sprintf("size() is compared with a number in 0..%d, not this %s", MaxSizeOperand, operandTypeName(value)),
			Err:    filter.ErrInvalidExpression,
		}
	}

	return nil
}

// maxTypeNameBytes bounds the type name an error message spells for an operand, as the root module's internal/typename does
// (a nested module may not import it): the name of a struct type constructed at run time (reflect.StructOf) lists all its fields
// and their tags.
const maxTypeNameBytes = 128

// operandTypeName returns the name of the type of v, as %T formats it, cut to [maxTypeNameBytes].
func operandTypeName(v any) string {
	if v == nil {
		return "<nil>"
	}

	name := reflect.TypeOf(v).String()
	if len(name) > maxTypeNameBytes {
		return name[:maxTypeNameBytes] + "..."
	}

	return name
}
