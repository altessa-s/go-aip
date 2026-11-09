// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"github.com/shopspring/decimal"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// ValidateDecimal checks BSON Decimal128 representability independently of neutral decimal parsing: value must convert to a
// Decimal128 exactly, as the filter translator converts a decimal literal.
func (s *Schema) ValidateDecimal(value decimal.Decimal) error {
	if _, ok := bson.ParseDecimal128FromBigInt(value.Coefficient(), int(value.Exponent())); !ok {
		return coreerrs.Wrap(filter.ErrFieldTypeMismatch, "decimal exceeds BSON Decimal128 representation")
	}

	return nil
}
