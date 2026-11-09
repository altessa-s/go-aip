// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"bytes"
	"cmp"
	"math"
	"time"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqltype"
	"github.com/altessa-s/go-aip/filter"
)

func comparison(op filter.Operator, n int) bool {
	switch op {
	case filter.OpEqual:
		return n == 0
	case filter.OpNotEqual:
		return n != 0
	case filter.OpLT:
		return n < 0
	case filter.OpLTE:
		return n <= 0
	case filter.OpGT:
		return n > 0
	case filter.OpGTE:
		return n >= 0
	default:
	}

	return false
}

func compareValue(a, c any, k sqlbase.Kind, b *budget) (int, error) {
	if err := b.Spend(1); err != nil {
		return 0, err
	}

	if x, ok := a.(arrayValue); ok {
		y, ok := c.(arrayValue)
		if !ok {
			return 0, typeError()
		}

		return compareArrays(x, y, k, b)
	}

	if k == sqlbase.JSON {
		return compareJSON(a, c, b)
	}

	if k == sqlbase.Float {
		return compareFloat(a, c, b)
	}

	if k == sqlbase.Numeric || k == sqlbase.Integer {
		x, err := sqltype.ParseNumber(a, b)
		if err != nil {
			return 0, err
		}

		y, err := sqltype.ParseNumber(c, b)
		if err != nil {
			return 0, err
		}

		if err = b.Spend(numberCost(x) + numberCost(y)); err != nil {
			return 0, err
		}

		return sqltype.CompareNumber(x, y), nil
	}

	return compareScalar(a, c, b)
}

func compareFloat(a, c any, b *budget) (int, error) {
	x, err := normalize(a, sqlbase.Float, nil, b)
	if err != nil {
		return 0, err
	}

	y, err := normalize(c, sqlbase.Float, nil, b)
	if err != nil {
		return 0, err
	}

	u, uok := x.(float64)

	v, vok := y.(float64)
	if !uok || !vok {
		return 0, typeError()
	}

	if math.IsNaN(u) {
		if math.IsNaN(v) {
			return 0, nil
		}

		return 1, nil
	}

	if math.IsNaN(v) {
		return -1, nil
	}

	return cmp.Compare(u, v), nil
}

func compareScalar(a, c any, b *budget) (int, error) {
	switch x := a.(type) {
	case string:
		y, ok := c.(string)
		if !ok {
			return 0, typeError()
		}

		if err := b.Spend(len(x) + len(y)); err != nil {
			return 0, err
		}

		return b.options.textCompare(x, y), nil
	case bool:
		y, ok := c.(bool)
		if !ok {
			return 0, typeError()
		}

		if x == y {
			return 0, nil
		}

		if x {
			return 1, nil
		}

		return -1, nil
	case []byte:
		y, ok := c.([]byte)
		if !ok {
			return 0, typeError()
		}

		if err := b.Spend(len(x) + len(y)); err != nil {
			return 0, err
		}

		return bytes.Compare(x, y), nil
	case time.Time:
		y, ok := c.(time.Time)
		if !ok {
			return 0, typeError()
		}

		return x.Compare(y), nil
	default:
		return 0, typeError()
	}
}
