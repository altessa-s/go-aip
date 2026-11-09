// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb

import (
	"strings"
	"time"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Schema combines neutral SQL fields with the MariaDB settings of [NewFilterTranslatorWithSchema]: the storage
// contract, the literal bounds, the timestamp precision, the text collation, the array aliases and the decimal domain.
// It is immutable and safe for concurrent use.
type Schema struct {
	fields *sqlbase.Schema
	opts   schemaOptions
	// digits is the fractional-second digits of opts.timestampPrecision.
	digits int
}

// maxAliasPrefixLen leaves room in a MariaDB identifier for the counter of the array aliases.
const maxAliasPrefixLen = sqlbase.MariaDBMaxIdentLen - 8

// NewSchema binds immutable SQL fields to MariaDB settings. A nil fields, a negative timestamp precision, a text
// collation or an array alias prefix that is not an unqualified identifier fitting MariaDB's limits fail with
// [filter.ErrInvalidOptions], as does an invalid decimal format.
func NewSchema(fields *sqlbase.Schema, opts ...SchemaOption) (*Schema, error) {
	if fields == nil {
		return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "nil SQL schema")
	}

	o := newSchemaOptions(opts...)
	if o.timestampPrecision < 0 || strings.Contains(o.textCollation, ".") || strings.Contains(o.arrayAliasPrefix, ".") {
		return nil, filter.ErrInvalidOptions
	}

	if err := sqlbase.ValidateIdent(o.textCollation); err != nil || len(o.textCollation) > sqlbase.MariaDBMaxIdentLen {
		return nil, filter.ErrInvalidOptions
	}

	// The array aliases append a counter, bounded by the filter's operation limit, to the prefix.
	if err := sqlbase.ValidateIdent(o.arrayAliasPrefix); err != nil || len(o.arrayAliasPrefix) > maxAliasPrefixLen {
		return nil, filter.ErrInvalidOptions
	}

	if err := o.decimalFormat.Validate(); err != nil {
		return nil, err
	}

	return &Schema{fields: fields, opts: *o, digits: timestampDigits(o.timestampPrecision)}, nil
}

// ContractMode reports whether this schema uses the listing storage contract.
func (s *Schema) ContractMode() bool { return s != nil && s.opts.contract }

// ValidatePolicy checks the shared field declarations against a filter policy.
func (s *Schema) ValidatePolicy(policy *filter.Policy) error {
	if s == nil {
		return coreerrs.Wrap(filter.ErrInvalidOptions, "nil MariaDB schema")
	}

	return s.fields.ValidatePolicy(policy)
}

// timestampDigits returns the fractional-second digits of a timestamp precision, at most microseconds; zero keeps
// microseconds.
func timestampDigits(precision time.Duration) int {
	if precision == 0 {
		return maxTimestampDigits
	}

	digits := 0
	for unit := time.Second; digits < maxTimestampDigits && precision%unit != 0; unit /= 10 {
		digits++
	}

	return digits
}
