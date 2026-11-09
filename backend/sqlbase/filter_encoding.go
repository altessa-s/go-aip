// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// renderValue renders an operand value: a decimal through the dialect's exact rendering unless a value encoder owns
// it, anything else through [FilterTranslator.value].
func (t *FilterTranslator) renderValue(v any) (string, error) {
	if d, ok := v.(decimal.Decimal); ok && t.encode == nil {
		dd, ok := t.dialect.(DecimalDialect)
		if !ok {
			return "", coreerrs.Wrap(filter.ErrUnsupportedType, "decimal operand")
		}

		return dd.DecimalValue(d, t.value)
	}

	return t.value(v)
}

// decimalSupported reports whether decimal operands can be rendered exactly.
func (t *FilterTranslator) decimalSupported() bool {
	if t.encode != nil {
		return true
	}

	_, ok := t.dialect.(DecimalDialect)

	return ok
}
