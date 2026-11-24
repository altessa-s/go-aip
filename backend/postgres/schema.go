// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"regexp"
	"slices"
	"strings"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

type field struct {
	column       string
	kind         sqlbase.Kind
	array        bool
	presence     bool
	json         bool
	path         []string
	foldContains bool
}

// Schema combines neutral SQL fields with PostgreSQL execution options.
type Schema struct {
	fields *sqlbase.Schema
	opts   schemaOptions
}

// NewSchema binds immutable SQL fields to PostgreSQL execution options.
func NewSchema(fields *sqlbase.Schema, opts ...SchemaOption) (*Schema, error) {
	if fields == nil {
		return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "nil SQL schema")
	}

	o := newSchemaOptions(opts...)
	if (o.textCollation != DefaultTextCollation && !o.textCompareSet) || o.textCompare == nil || o.textCollation == "" ||
		strings.ContainsRune(o.textCollation, 0) || len(o.textCollation) > sqlbase.PostgresMaxIdentLen ||
		!identifier.MatchString(o.arrayAliasPrefix) || len(o.arrayAliasPrefix) > maxAliasPrefixLen || o.timestampPrecision < 0 {
		return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "invalid PostgreSQL semantic options")
	}

	o.caseInsensitiveContainsFields = slices.Clone(o.caseInsensitiveContainsFields)
	slices.Sort(o.caseInsensitiveContainsFields)

	o.caseInsensitiveContainsFields = slices.Compact(o.caseInsensitiveContainsFields)
	for _, name := range o.caseInsensitiveContainsFields {
		f, err := fields.Field(name)
		if err != nil {
			return nil, err
		}

		if f.Kind() != sqlbase.Text || f.IsJSONPath() {
			return nil, coreerrs.Wrapf(filter.ErrInvalidOptions, "case-insensitive contains requires a text column for %q", name)
		}
	}

	return &Schema{fields: fields, opts: *o}, nil
}

// ContractMode reports whether this schema uses the listing storage contract.
func (s *Schema) ContractMode() bool { return s != nil && s.opts.contract }

// ValidatePolicy checks the shared field declarations against a filter policy.
func (s *Schema) ValidatePolicy(policy *filter.Policy) error {
	if s == nil {
		return coreerrs.Wrap(filter.ErrInvalidOptions, "nil PostgreSQL schema")
	}

	if err := s.fields.ValidatePolicy(policy); err != nil {
		return err
	}

	for _, name := range s.opts.caseInsensitiveContainsFields {
		if _, enum := policy.EnumValues(name); enum {
			return coreerrs.Wrapf(filter.ErrInvalidOptions, "case-insensitive contains disallows enum field %q", name)
		}
	}

	return nil
}
func (s *Schema) field(name string) (field, error) {
	var (
		fields       *sqlbase.Schema
		foldContains bool
	)

	if s != nil {
		fields = s.fields
		_, foldContains = slices.BinarySearch(s.opts.caseInsensitiveContainsFields, name)
	}

	f, err := fields.Field(name)

	return field{column: f.Column(), kind: f.Kind(), array: f.IsArray(), presence: f.HasPresence(), json: f.IsJSONPath(), path: f.Path(),
		foldContains: foldContains}, err
}

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// maxAliasPrefixLen leaves room in a PostgreSQL identifier for the counter suffix of the array aliases: it is bounded by the
// filter's operation limit, far below seven digits.
const maxAliasPrefixLen = sqlbase.PostgresMaxIdentLen - 8

// quoteColumn quotes each dot-separated part of a schema column. A part longer than PostgreSQL's identifier limit is
// rejected: the server would silently truncate it and read another column.
func quoteColumn(name string) (string, error) {
	parts := strings.Split(name, ".")
	for i, p := range parts {
		if p == "" || strings.ContainsRune(p, 0) {
			return "", coreerrs.Wrap(filter.ErrInvalidExpression, "invalid SQL identifier")
		}

		if len(p) > sqlbase.PostgresMaxIdentLen {
			return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "SQL identifier %q exceeds %d bytes", p, sqlbase.PostgresMaxIdentLen)
		}

		parts[i] = `"` + strings.ReplaceAll(p, `"`, `""`) + `"`
	}

	return strings.Join(parts, "."), nil
}
func typeName(k sqlbase.Kind) string {
	switch k {
	case sqlbase.Boolean:
		return "boolean"
	case sqlbase.Integer:
		return "bigint"
	case sqlbase.Float:
		return "double precision"
	case sqlbase.Text:
		return "text"
	case sqlbase.Bytes:
		return "bytea"
	case sqlbase.Timestamp:
		return "timestamptz"
	case sqlbase.Numeric:
		return "numeric"
	case sqlbase.JSON:
		return "jsonb"
	case sqlbase.UUID:
		return "uuid"
	default:
	}

	return ""
}
