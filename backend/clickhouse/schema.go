// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse

import (
	"time"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Schema combines neutral SQL fields with the ClickHouse settings of [NewFilterTranslatorWithSchema]: the storage
// contract, the literal budget, the timestamp precision and the decimal domain. It is immutable and safe for
// concurrent use.
type Schema struct {
	fields *sqlbase.Schema
	opts   schemaOptions
	// digits is the fractional-second digits of opts.timestampPrecision.
	digits int
}

// NewSchema binds immutable SQL fields to ClickHouse settings. A nil fields or a negative timestamp precision fails
// with [filter.ErrInvalidOptions], as does an invalid decimal format.
func NewSchema(fields *sqlbase.Schema, opts ...SchemaOption) (*Schema, error) {
	if fields == nil {
		return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "nil SQL schema")
	}

	o := newSchemaOptions(opts...)
	if o.timestampPrecision < 0 {
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
		return coreerrs.Wrap(filter.ErrInvalidOptions, "nil ClickHouse schema")
	}

	return s.fields.ValidatePolicy(policy)
}

// timestampDigits returns the fractional-second digits of a timestamp precision, at most nanoseconds; zero keeps
// nanoseconds.
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
