// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb

import (
	"time"

	"github.com/altessa-s/go-aip/backend/sqlbase"
)

//go:generate optgen generate --type=schemaOptions --option-type=SchemaOption --output=schema_options_gen.go

type schemaOptions struct {
	// WithSchemaContractMode renders against the listing storage contract. [NewFilterTranslatorWithSchema] requires it.
	contract bool `opt:"SchemaContractMode" optval:"param"`
	// WithSchemaMaxValueDepth bounds pointer unwrapping during literal validation.
	maxValueDepth int `opt:"SchemaMaxValueDepth" optgen:"default=DefaultMaxValueDepth" optval:"positive"`
	// WithSchemaMaxArrayDimensions bounds nested arrays in a filter literal.
	maxArrayDimensions int `opt:"SchemaMaxArrayDimensions" optgen:"default=DefaultMaxArrayDimensions" optval:"positive"`
	// WithSchemaMaxDataBudget bounds literal validation work, in bytes and values.
	maxDataBudget int `opt:"SchemaMaxDataBudget" optgen:"default=DefaultMaxDataBudget" optval:"positive"`
	// WithSchemaTimestampPrecision truncates timestamp operands and sets the fractional digits of their DATETIME casts:
	// the digits the precision needs (1ms renders DATETIME(3)), at most six. Zero keeps microseconds; a negative
	// precision fails with filter.ErrInvalidOptions.
	timestampPrecision time.Duration `opt:"SchemaTimestampPrecision" optgen:"default=DefaultTimestampPrecision" optval:"nonpositive"`
	// WithSchemaTextCollation chooses the collation of text array elements: an unquoted identifier of at most 64 bytes.
	textCollation string `opt:"SchemaTextCollation" optgen:"default=DefaultTextCollation"`
	// WithSchemaArrayAliasPrefix controls the JSON_TABLE aliases of array fields: the prefix followed by a counter.
	arrayAliasPrefix string `opt:"SchemaArrayAliasPrefix" optgen:"default=DefaultArrayAliasPrefix"`
	// WithSchemaDecimalFormat sets the DECIMAL(P, S) domain decimal operands are cast to. Every operand must fit it
	// exactly.
	decimalFormat sqlbase.DecimalFormat `opt:"SchemaDecimalFormat" optgen:"default=sqlbase.DefaultDecimalFormat()"`
}

// DefaultTextCollation is the default collation for text comparisons.
const DefaultTextCollation = "utf8mb4_nopad_bin"

// DefaultArrayAliasPrefix prefixes generated array element aliases.
const DefaultArrayAliasPrefix = "_aip_element_"

// DefaultMaxValueDepth bounds pointer and value nesting.
const DefaultMaxValueDepth = 64

// DefaultMaxArrayDimensions bounds nested arrays.
const DefaultMaxArrayDimensions = 6

// DefaultMaxDataBudget bounds literal validation work.
const DefaultMaxDataBudget = 1 << 22

// DefaultTimestampPrecision is the default timestamp resolution, DATETIME(3).
const DefaultTimestampPrecision = time.Millisecond

// maxTimestampDigits is the finest DATETIME precision, microseconds.
const maxTimestampDigits = 6
