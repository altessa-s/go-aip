// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/filter"
)

// DecimalFormat describes the exact fixed-point domain DECIMAL(Precision,Scale) of stored decimal columns, used by the
// MariaDB and ClickHouse filter casts and list cursors. Every stored decimal column must fit this domain; use a separate
// translator or list for schemas requiring a different one. Validate and Check apply MariaDB's bounds (precision 1..65,
// scale 0..38), which ClickHouse's Decimal(P, S) also accepts, to both backends; SQL renders the MariaDB type.
type DecimalFormat struct{ Precision, Scale int }

// DefaultDecimalFormat returns the default DECIMAL(65,30) domain.
func DefaultDecimalFormat() DecimalFormat {
	const precision, scale = 65, 30
	return DecimalFormat{Precision: precision, Scale: scale}
}

// Validate rejects invalid MariaDB precision and scale.
func (f DecimalFormat) Validate() error {
	if f.Precision < 1 || f.Precision > 65 || f.Scale < 0 || f.Scale > 38 || f.Scale > f.Precision {
		return filter.ErrInvalidOptions
	}

	return nil
}

// SQL returns a validated SQL type expression.
func (f DecimalFormat) SQL() (string, error) {
	if err := f.Validate(); err != nil {
		return "", err
	}

	return fmt.Sprintf("DECIMAL(%d,%d)", f.Precision, f.Scale), nil
}

// Check rejects values that a cast would round or overflow.
func (f DecimalFormat) Check(v any) error {
	if err := f.Validate(); err != nil {
		return err
	}

	d, err := filter.DecimalLiteral(v)
	if err != nil {
		return err
	}

	if !d.Equal(d.Truncate(int32(f.Scale))) || d.Abs().GreaterThanOrEqual(decimal.New(1, int32(f.Precision-f.Scale))) {
		return filter.ErrFieldTypeMismatch
	}

	return nil
}
