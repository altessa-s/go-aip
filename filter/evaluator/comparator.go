// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"errors"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/filter"
)

// Comparator overrides scalar comparison, including built-in strings and numbers.
// handled=false delegates to the built-in comparator. Implementations must be concurrency safe.
type Comparator interface {
	Compare(filter.Operator, any, any) (matched, handled bool, err error)
}

func (e *evaluation) compare(op filter.Operator, left, right any) (bool, error) {
	if e.config.comparator != nil {
		matched, handled, err := e.config.comparator.Compare(op, left, right)
		if handled || err != nil {
			return matched, err
		}
	}

	if left == nil || right == nil {
		return compareNil(op, left, right)
	}

	_, leftDecimal := left.(decimal.Decimal)

	_, rightDecimal := right.(decimal.Decimal)
	if leftDecimal || rightDecimal {
		l, err := e.config.DecimalOperand(nil, left)
		if err != nil {
			return false, errors.Join(filter.ErrUnsupportedType, err)
		}

		r, err := e.config.DecimalOperand(nil, right)
		if err != nil {
			return false, errors.Join(filter.ErrUnsupportedType, err)
		}

		return CompareResult(op, l.Cmp(r))
	}

	return compare(op, left, right)
}
