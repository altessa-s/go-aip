// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"strings"
	"time"
)

// DefaultTextCollation is the default collation for text comparisons.
const DefaultTextCollation = "C"

// DefaultArrayAliasPrefix prefixes generated array element aliases.
const DefaultArrayAliasPrefix = "_aip_element_"

//go:generate optgen generate --type=schemaOptions --option-type=SchemaOption --output=schema_options_gen.go

type schemaOptions struct {
	// narrowIntegers widens stored integer arrays to match the filter's bigint arrays.
	narrowIntegers bool `opt:"-"`
	// textCompareSet is written by generated WithSchemaTextCompare to distinguish an explicit callback from the default.
	textCompareSet bool `opt:"-"`
	// WithSchemaMaxValueDepth bounds pointer unwrapping and nested JSON values.
	maxValueDepth int `opt:"SchemaMaxValueDepth" optgen:"default=DefaultMaxValueDepth" optval:"positive"`
	// WithSchemaMaxArrayDimensions bounds nested arrays accepted by the profile.
	maxArrayDimensions int `opt:"SchemaMaxArrayDimensions" optgen:"default=DefaultMaxArrayDimensions" optval:"positive"`
	// WithSchemaTimestampPrecision controls timestamp normalization; zero preserves precision.
	timestampPrecision time.Duration `opt:"SchemaTimestampPrecision" optgen:"default=DefaultTimestampPrecision" optval:"nonpositive"`
	// WithSchemaTextCollation chooses the SQL collation. Configure the matching WithSchemaTextCompare callback as well.
	textCollation string `opt:"SchemaTextCollation" optgen:"default=DefaultTextCollation"`
	// WithSchemaTextCompare supplies the ordering matching WithSchemaTextCollation.
	textCompare func(string, string) int `opt:"SchemaTextCompare" optgen:"default=defaultTextCompare,enables=textCompareSet"`
	// WithSchemaArrayAliasPrefix controls generated array element aliases: the prefix followed by a counter.
	arrayAliasPrefix string `opt:"SchemaArrayAliasPrefix" optgen:"default=DefaultArrayAliasPrefix"`

	// WithSchemaCaseInsensitiveContainsFields enables Unicode 15.1 simple lowercase substring matching for the named text columns.
	caseInsensitiveContainsFields []string `opt:"SchemaCaseInsensitiveContainsFields" optval:"clone=eager,notrim,nodup"`

	// WithSchemaMaxDataBudget bounds work spent compiling literal values.
	maxDataBudget int `opt:"SchemaMaxDataBudget" optgen:"default=DefaultMaxDataBudget" optval:"positive"`
	// WithSchemaContractMode renders against non-null, C-collated listing columns. It is the only contract switch:
	// [NewFilterTranslatorWithSchema] follows the schema.
	contract bool `opt:"SchemaContractMode" optval:"param"`
	// WithSchemaExplicitCollation keeps the explicit text collation in contract mode, for listing columns stored in
	// another collation.
	explicitCollation bool `opt:"SchemaExplicitCollation" optval:"param"`
	// WithSchemaJSONBStringCompare must reproduce the database's default collation, including
	// deterministic tie-breaking. strings.Compare is suitable for a C-locale database.
	// Without an explicit comparator, ordering whole JSONB values is rejected.
	jsonbStringCompare func(string, string) int `opt:"SchemaJSONBStringCompare"`
}

func defaultTextCompare(a, b string) int { return strings.Compare(a, b) }

// DefaultMaxValueDepth bounds pointer and JSON nesting.
const DefaultMaxValueDepth = 64

// DefaultMaxArrayDimensions bounds nested arrays.
const DefaultMaxArrayDimensions = 6

// DefaultTimestampPrecision is the default resolution of schema timestamp operands, microseconds like timestamptz. It is
// unrelated to the list engine's cursor precision, which defaults to listing.TimestampPrecision (milliseconds) and is set
// with sqlbase.WithListTimestampPrecision.
const DefaultTimestampPrecision = time.Microsecond
