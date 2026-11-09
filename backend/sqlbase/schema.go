// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/altessa-s/go-aip/filter"

	coremaps "github.com/altessa-s/go-atlas-core/collections/maps"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Kind identifies a SQL value class. Numeric is independent of BSON Decimal128.
type Kind uint8

// SQL value kinds. Their database representation is chosen by each dialect.
const (
	Unknown Kind = iota
	Boolean
	Integer
	Float
	Text
	Bytes
	Timestamp
	Numeric
	JSON
	UUID
)

// Field describes a SQL column or an explicitly typed JSON path. Use the constructors;
// its zero value is invalid. Descriptors are immutable.
type Field struct {
	column   string
	kind     Kind
	array    bool
	presence bool
	json     bool
	path     []string
}

// Scalar describes a scalar column.
func Scalar(column string, kind Kind) Field { return Field{column: column, kind: kind} }

// PresenceScalar describes a nullable scalar column with explicit presence.
func PresenceScalar(column string, kind Kind) Field {
	return Field{column: column, kind: kind, presence: true}
}

// Array describes an array column.
func Array(column string, element Kind) Field {
	return Field{column: column, kind: element, array: true}
}

// JSONPath describes a typed value within a JSON column. Interpretation of path
// components belongs to the consuming dialect.
func JSONPath(column string, kind Kind, path ...string) Field {
	return Field{column: column, kind: kind, json: true, path: slices.Clone(path)}
}

// Schema is an immutable map from CEL field names to SQL storage descriptors.
// The same schema must be supplied to translation and in-memory evaluation.
type Schema struct {
	fields        *coremaps.ImmutableMap[string, Field]
	kindValidator func(filter.FieldKind, Kind) bool
}

var schemaColumnPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// NewSchema validates and freezes fields. Column names are identifiers, never SQL expressions.
func NewSchema(fields map[string]Field, opts ...SchemaOption) (*Schema, error) {
	o := newSchemaOptions(opts...)
	if o.columnValidator == nil || o.kindValidator == nil {
		return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "nil column or kind validator")
	}

	frozen := make(map[string]Field, len(fields))
	for name, f := range fields {
		if name == "" || f.kind <= Unknown || f.kind > UUID || f.array && f.json {
			return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "invalid SQL field descriptor")
		}

		if err := o.columnValidator(f.column); err != nil {
			return nil, err
		}

		for _, part := range f.path {
			if !utf8.ValidString(part) || strings.ContainsRune(part, 0) {
				return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "NUL in JSON path")
			}
		}

		f.path = slices.Clone(f.path)
		frozen[name] = f
	}

	return &Schema{fields: coremaps.NewImmutableMap(frozen), kindValidator: o.kindValidator}, nil
}
func validateColumn(name string) error {
	for part := range strings.SplitSeq(name, ".") {
		if !schemaColumnPattern.MatchString(part) {
			return coreerrs.Wrap(filter.ErrInvalidExpression, "invalid SQL identifier")
		}
	}

	return nil
}

// Field looks up a descriptor, returning ErrInvalidOptions for an undeclared name.
func (s *Schema) Field(name string) (Field, error) {
	if s != nil && s.fields != nil {
		if f, ok := s.fields.Get(name); ok {
			return f, nil
		}
	}

	return Field{}, coreerrs.Wrapf(filter.ErrInvalidOptions, "no SQL type declared for field %q", name)
}

// ValidatePolicy rejects ambiguous mappings and incompatible declarations before use.
func (s *Schema) ValidatePolicy(policy *filter.Policy) error {
	if s == nil || s.fields == nil || policy == nil {
		return coreerrs.Wrap(filter.ErrInvalidOptions, "nil SQL schema or policy")
	}

	for name, f := range s.fields.All() {
		mapped := policy.ApplyFieldMapping(name)
		if mapped != name && mapped != f.column {
			return coreerrs.Wrapf(filter.ErrInvalidOptions, "conflicting mapping for %q", name)
		}

		kind := policy.FieldKind(name)
		if kind != filter.FieldKindUnspecified && !s.kindValidator(kind, f.kind) {
			return coreerrs.Wrapf(filter.ErrInvalidOptions, "conflicting type for %q", name)
		}

		if policy.IsArrayField(name) && !f.array {
			return coreerrs.Wrapf(filter.ErrInvalidOptions, "conflicting array declaration for %q", name)
		}
	}

	return nil
}
func commonKind(k Kind) filter.FieldKind {
	switch k {
	case Boolean:
		return filter.FieldKindBool
	case Integer:
		return filter.FieldKindInt
	case Float:
		return filter.FieldKindFloat
	case Text, UUID:
		return filter.FieldKindString
	case Bytes:
		return filter.FieldKindBytes
	case Timestamp:
		return filter.FieldKindTimestamp
	case Numeric:
		return filter.FieldKindDecimal
	default:
		return filter.FieldKindUnspecified
	}
}

// Column returns the qualified storage column.
func (f Field) Column() string { return f.column }

// Kind returns the scalar or array element kind.
func (f Field) Kind() Kind { return f.kind }

// IsArray reports whether the field stores an array.
func (f Field) IsArray() bool { return f.array }

// HasPresence reports whether the field has explicit nullable presence.
func (f Field) HasPresence() bool { return f.presence }

// IsJSONPath reports whether the field extracts a JSON path.
func (f Field) IsJSONPath() bool { return f.json }

// Path returns a copy of the JSON path components.
func (f Field) Path() []string { return slices.Clone(f.path) }
