// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse

import (
	"time"

	"github.com/altessa-s/go-aip/backend/sqlbase"
)

//go:generate optgen generate --type=schemaOptions --option-type=SchemaOption --output=schema_options_gen.go

type schemaOptions struct {
	// WithSchemaContractMode renders against the listing storage contract. [NewFilterTranslatorWithSchema] requires it.
	contract bool `opt:"SchemaContractMode" optval:"param"`
	// WithSchemaMaxDataBudget bounds literal validation work, in bytes and values.
	maxDataBudget int `opt:"SchemaMaxDataBudget" optgen:"default=DefaultMaxDataBudget" optval:"positive"`
	// WithSchemaTimestampPrecision truncates timestamp operands and sets the fractional digits of their DateTime64
	// conversions: the digits the precision needs (1ms renders scale 3), at most nine. Zero keeps nanoseconds; a
	// negative precision fails with filter.ErrInvalidOptions.
	timestampPrecision time.Duration `opt:"SchemaTimestampPrecision" optgen:"default=DefaultTimestampPrecision" optval:"nonpositive"`
	// WithSchemaDecimalFormat sets the Decimal(P, S) domain decimal operands are cast to. Every operand must fit it
	// exactly.
	decimalFormat sqlbase.DecimalFormat `opt:"SchemaDecimalFormat" optgen:"default=sqlbase.DefaultDecimalFormat()"`
}

// DefaultMaxDataBudget bounds the literal validation work of the schema translator.
const DefaultMaxDataBudget = 1 << 22
