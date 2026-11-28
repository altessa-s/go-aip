// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

//go:generate optgen generate --type=listOptions --option-type=ListEngineOption --option-prefix=List --output=list_options_gen.go

import (
	"strings"
	"time"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

// DefaultMaxTime is the default time limit of one list query.
const DefaultMaxTime = 10 * time.Second

// DefaultIDColumn is the default name of the identity column.
const DefaultIDColumn = "id"

// DefaultTableAlias qualifies sort and cursor columns.
const DefaultTableAlias = "_listing"

// ArrayStorage selects how array fields are stored by MariaDB.
type ArrayStorage uint8

const (
	// ArraysUnsupported rejects schemas containing array fields.
	ArraysUnsupported ArrayStorage = iota
	// ArraysJSON stores array values in JSON columns.
	ArraysJSON
)

type listOptions struct {
	// transaction shares a ListTransaction across engines or with other transaction operations.
	transaction *ListTransaction

	// kindResolver maps public kinds to SQL storage classes before the dialect builds its translator.
	kindResolver func(filter.FieldKind) Kind `optgen:"default=DefaultSQLKind"`

	// tableAlias qualifies sort and cursor columns.
	tableAlias string `optgen:"default=DefaultTableAlias"`
	// textCollation overrides the dialect's expected text collation; empty uses its default.
	textCollation string
	// schemaCheckPolicy replaces individual built-in schema checks; nil preserves strict defaults.
	schemaCheckPolicy ListSchemaCheckPolicy
	// indexNamer returns an unquoted index identifier for a table and zero-based pattern number.
	indexNamer IndexNamer
	// idKind is the scalar class of the unique tie-breaker column.
	idKind filter.FieldKind `optgen:"default=filter.FieldKindString"`
	// timestampPrecision is the cursor timestamp resolution; stored timestamps must be aligned.
	timestampPrecision time.Duration           `optgen:"default=listing.TimestampPrecision"`
	positionCodec      pagetoken.PositionCodec `optgen:"default=pagetoken.ScalarPositionCodec{},notnil"`

	decimalFormat DecimalFormat `optgen:"default=DefaultDecimalFormat()"`
	// parentKey explicitly binds a declared scope key to Request.Parent. Empty disables parent binding.
	parentKey string `optfn:"ParentKey=parentKeyValue"`
	// errorMapper overrides query failure classification. It receives the query error; a Scan failing after the query context
	// ended arrives as that context's error, the scan error hidden behind apierror.Classify.
	errorMapper listing.ErrorMapper

	columns map[string]string
	// uuidFields are string fields stored in uuid columns, as IDUUID stores the identity.
	uuidFields []string `optgen:"strings,unique,trim"`
	// tableExpression is a trusted derived table that List and Count read in place of the table, such as a join adding
	// columns of another table. Schema checks and indexes keep using the table.
	tableExpression string
	// derivedColumns are the columns only tableExpression has; schema checks do not look for them in the table.
	derivedColumns []string `optgen:"strings,unique,trim"`
	// narrowIntegers accepts narrower signed integer columns for integer fields, such as PostgreSQL smallint and integer.
	narrowIntegers bool
	// explicitCollation makes filters compare text under the expected collation explicitly, so only sort, scope and
	// identity columns must store it.
	explicitCollation bool
	idColumn          string        `optgen:"default=DefaultIDColumn"`
	idType            IDType        `optgen:"default=IDNative"`
	scopeKeys         []string      `optgen:"strings,unique,trim"`
	maxTime           time.Duration `optgen:"default=DefaultMaxTime" optval:"nonpositive"`
	// dialect is the required dialect argument of NewListEngine.
	dialect      ListDialect  `opt:"-"`
	arrayStorage ArrayStorage `opt:"ArrayStorage"`
}

func parentKeyValue(v string) string { return strings.TrimSpace(v) }
