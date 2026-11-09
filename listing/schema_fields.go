// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing

import (
	"fmt"
	"maps"
	"regexp"
	"slices"

	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/filter"

	coremaps "github.com/altessa-s/go-atlas-core/collections/maps"
	coreslices "github.com/altessa-s/go-atlas-core/collections/slices"
)

// Field declares one filterable field of a list by its public name. Where an
// engine stores it (a BSON path, a column) is the engine's mapping.
type Field struct {
	// Name is the public name of the field in filter and orderBy
	// (lowerCamelCase, as in the proto JSON), e.g. "createTime".
	Name string
	// Kind is the declared type: filter literals must match it
	// (timestamps through timestamp("...")). It is required.
	Kind filter.FieldKind
	// Enum, when set, makes the field an enum: a filter spells its values
	// by their proto enum names (the keys), compares them only with ==, !=
	// and in, and the names are rewritten to immutable stored values validated
	// by the configured field kind and value codec.
	Enum map[string]any
	// EnumZero is the proto name mapped to "" (string) or int64(0) (integer)
	// for an optional enum. Arbitrary missing-value substitutions are not supported
	// by the portable listing contract.
	EnumZero string
	// Array declares a list-valued field: a filter tests membership
	// (field == x or x in field matches when any element equals x). An
	// array field is never part of a sort pattern.
	Array bool
	// Optional declares a field that may be absent or null in a stored
	// item. It is filterable but never part of a sort pattern: keyset
	// paging needs a comparable value in every sort field of every item.
	Optional bool
	// Presence declares an explicitly present proto3 optional or wrapper field.
	// Such fields may be null, with the default evaluator's null semantics:
	// null differs from every value, while an ordered comparison with it is
	// unknown.
	Presence bool
	// Hidden declares a field the client never reads (it is left out of
	// every response): it is filterable but, unless the schema allows it
	// ([WithUnreadableSortAllowed]), never part of a sort pattern, because a
	// page token carries the sort values of the last item of a page.
	Hidden bool
}

func enumZeroMatches(enum map[string]any, name string, kind filter.FieldKind) bool {
	v, ok := enum[name]
	if !ok {
		return false
	}

	switch kind {
	case filter.FieldKindString:
		return v == ""
	case filter.FieldKindInt:
		return v == int64(0)
	default:
		return false
	}
}

// FieldInfo is the declaration of a listing field without its enum map, returned by [Schema.FieldInfo].
type FieldInfo struct {
	// EnumZeroValue is the stored value of EnumZero (nil when EnumZero is empty).
	EnumZeroValue any
	// Name is the public field name.
	Name string
	// EnumZero is the proto enum name stored as the zero value of an optional enum.
	EnumZero string
	// Kind is the declared field kind.
	Kind filter.FieldKind
	// Enum reports an enum field.
	Enum bool
	// Array reports a list-valued field.
	Array bool
	// Optional reports a field whose absent or null value reads as its kind's zero.
	Optional bool
	// Presence reports an explicitly present nullable field.
	Presence bool
	// Hidden reports a field the client never reads.
	Hidden bool
}

// compiledField is a validated [Field].
type compiledField struct {
	Field
	enum     *coremaps.ImmutableMap[string, any] // proto enum name -> stored value (normalized)
	enumZero any                                 // stored value of EnumZero
}

// fieldName is the syntax of a public field name: dotted identifiers.
var fieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

func (s *Schema) compileFields(fields []Field) error {
	if len(fields) == 0 && !s.opts.emptyFieldsAllowed {
		return invalid("no fields")
	}

	compiled := make(map[string]compiledField, len(fields))
	for _, f := range fields {
		cf, err := s.compileField(f, compiled)
		if err != nil {
			return err
		}

		compiled[f.Name] = cf
	}

	s.fields = coremaps.NewImmutableMap(compiled)

	return nil
}

// compileField validates the declaration of one field against the fields compiled before it and compiles its enum.
func (s *Schema) compileField(f Field, compiled map[string]compiledField) (compiledField, error) {
	if err := s.checkField(f, compiled); err != nil {
		return compiledField{}, err
	}

	cf := compiledField{Field: f}
	if f.Enum == nil {
		return cf, nil
	}

	enum, err := compileEnum(f, s.opts.filterOptions...)
	if err != nil {
		return compiledField{}, err
	}

	cf.enum = coremaps.NewImmutableMap(enum)
	cf.Enum = nil
	cf.enumZero = enum[f.EnumZero]

	if f.Optional && !enumZeroMatches(enum, f.EnumZero, f.Kind) {
		return compiledField{}, invalid("field %q: EnumZero must store the kind's zero value", f.Name)
	}

	return cf, nil
}

// checkField validates the name, uniqueness, kind and attribute combination of a field declaration.
func (s *Schema) checkField(f Field, compiled map[string]compiledField) error {
	if s.opts.fieldNameValidator != nil {
		if err := s.opts.fieldNameValidator(f.Name); err != nil {
			return invalid("field name %q: %v", f.Name, err)
		}
	}

	switch _, dup := compiled[f.Name]; {
	case dup:
		return invalid("field %q declared twice", f.Name)
	case f.Kind == filter.FieldKindUnspecified:
		return invalid("field %q: kind is required", f.Name)
	case f.Presence && (f.Optional || f.Array):
		return invalid("field %q: Presence is incompatible with Optional and Array", f.Name)
	case f.Optional && f.Enum != nil && f.EnumZero == "":
		return invalid("field %q: EnumZero is required for an optional enum", f.Name)
	case f.EnumZero != "" && !f.Optional:
		return invalid("field %q: EnumZero is only valid for an optional enum", f.Name)
	default:
		return nil
	}
}

// compileEnum validates immutable stored values through the configured field policy.
func compileEnum(f Field, opts ...filter.PolicyOption) (map[string]any, error) {
	if len(f.Enum) == 0 {
		return nil, invalid("field %q: empty enum", f.Name)
	}

	policy, err := filter.NewPolicy(append([]filter.PolicyOption{filter.WithAllowedFields(f.Name),
		filter.WithFieldTypes(map[string]filter.FieldKind{f.Name: f.Kind})}, opts...)...)
	if err != nil {
		return nil, invalid("field %q: enum policy: %v", f.Name, err)
	}

	out := make(map[string]any, len(f.Enum))
	for name, value := range f.Enum {
		stored, _, ok := fieldpolicy.NormalizeEnumValue(value)
		if !ok {
			return nil, invalid("field %q: enum %s stores mutable or nil value", f.Name, name)
		}

		if err := policy.CheckLiteralKind(f.Name, &filter.LiteralNode{Value: stored}); err != nil {
			return nil, invalid("field %q: enum %s: %v", f.Name, name, err)
		}

		out[name] = stored
	}

	return out, nil
}

func defaultFieldNameValidator(name string) error {
	if !fieldName.MatchString(name) {
		return fmt.Errorf("expected dotted identifiers")
	}

	return nil
}

// Fields returns the public names of the filter fields, sorted.
func (s *Schema) Fields() []string { return slices.Sorted(s.fields.Keys()) }

// OptionalFields returns the declared fields whose empty stored values may be
// omitted by an engine's storage encoding.
func (s *Schema) OptionalFields() []string {
	fields := make([]string, 0)
	for name, field := range s.fields.All() {
		fields = coreslices.AppendIf(fields, field.Optional, name)
	}

	slices.Sort(fields)

	return fields
}

// PresenceFields returns the explicitly present nullable fields, sorted by name.
func (s *Schema) PresenceFields() []string {
	fields := make([]string, 0)
	for name, field := range s.fields.All() {
		fields = coreslices.AppendIf(fields, field.Presence, name)
	}

	slices.Sort(fields)

	return fields
}

// ZeroFields returns the optional scalar fields and their kinds. An absent or
// null value of one of these fields reads as the kind's zero value.
func (s *Schema) ZeroFields() map[string]filter.FieldKind {
	fields := make(map[string]filter.FieldKind)
	for name, field := range s.fields.All() {
		if field.Optional && !field.Array {
			fields[name] = field.Kind
		}
	}

	return fields
}

// Field returns the declared field of a public name.
func (s *Schema) Field(name string) (Field, bool) {
	f, ok := s.fields.Get(name)
	return s.fieldMetadata(f), ok
}

// FieldInfo returns the declaration of the field name without its enum map. Unlike [Schema.Field] it copies nothing, so engines
// that read field metadata on every item or predicate use it. It reports false for an undeclared field and for a nil schema.
func (s *Schema) FieldInfo(name string) (FieldInfo, bool) {
	if s == nil {
		return FieldInfo{}, false
	}

	f, ok := s.fields.Get(name)
	if !ok {
		return FieldInfo{}, false
	}

	return FieldInfo{
		EnumZeroValue: f.enumZero, Name: f.Name, EnumZero: f.EnumZero, Kind: f.Kind, Enum: f.enum != nil,
		Array: f.Array, Optional: f.Optional, Presence: f.Presence, Hidden: f.Hidden,
	}, true
}

// fieldMetadata returns detached enum declarations for application hooks.
func (s *Schema) fieldMetadata(f compiledField) Field {
	out := f.Field
	if f.enum != nil {
		out.Enum = maps.Collect(f.enum.All())
	}

	return out
}
