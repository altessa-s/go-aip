// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"slices"
	"strings"

	"github.com/altessa-s/go-aip/filter"

	coremaps "github.com/altessa-s/go-atlas-core/collections/maps"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Schema holds MongoDB-specific storage interpretation rules independently
// from the shared filter field and expression policy. It is immutable and safe
// for concurrent use.
type Schema struct {
	zeroWhenAbsent     *coremaps.ImmutableMap[string, filter.FieldKind]
	omitEmpty          *coremaps.ImmutableMap[string, struct{}]
	omitEmptyPaths     *coremaps.ImmutableMap[string, struct{}]
	sizeZeroWhenAbsent bool
	presenceFields     *coremaps.ImmutableMap[string, struct{}]
}

// NewSchema validates and freezes MongoDB storage rules. Cross-checks against
// shared field declarations happen when the schema is bound to a filter policy.
func NewSchema(opts ...SchemaOption) (*Schema, error) {
	o := newSchemaOptions(opts...)
	for field, kind := range o.zeroWhenAbsent {
		if strings.TrimSpace(field) == "" || kind == filter.FieldKindUnspecified || kind > filter.FieldKindDecimal {
			return nil, coreerrs.Wrapf(filter.ErrInvalidOptions, "invalid zero-when-absent declaration for field %q", field)
		}
	}

	for _, field := range o.omitEmpty {
		if strings.TrimSpace(field) == "" {
			return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "empty omitempty field name")
		}
	}

	presence := make(map[string]struct{}, len(o.presenceFields))
	for _, field := range o.presenceFields {
		if strings.TrimSpace(field) == "" {
			return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "empty presence field name")
		}

		if _, ok := o.zeroWhenAbsent[field]; ok {
			return nil, coreerrs.Wrapf(filter.ErrInvalidOptions, "field %q is both presence and zero-when-absent", field)
		}

		if slices.Contains(o.omitEmpty, field) {
			return nil, coreerrs.Wrapf(filter.ErrInvalidOptions, "field %q is both presence and omitempty", field)
		}

		presence[field] = struct{}{}
	}

	zero := coremaps.NewImmutableMap(o.zeroWhenAbsent)

	omit := make(map[string]struct{}, len(o.omitEmpty))
	for _, field := range o.omitEmpty {
		omit[field] = struct{}{}
	}

	return &Schema{
		zeroWhenAbsent:     zero,
		omitEmpty:          coremaps.NewImmutableMap(omit),
		sizeZeroWhenAbsent: o.sizeZeroWhenAbsent,
		presenceFields:     coremaps.NewImmutableMap(presence),
	}, nil
}

// DefaultSchema returns a schema with no MongoDB storage rules.
func DefaultSchema() *Schema {
	schema, _ := NewSchema()
	return schema
}

// ValidatePolicy rejects conflicting shared field declarations.
func (s *Schema) ValidatePolicy(policy *filter.Policy) error {
	if s == nil || policy == nil {
		return coreerrs.Wrap(filter.ErrInvalidOptions, "nil MongoDB schema or filter policy")
	}

	for field, kind := range s.zeroWhenAbsent.All() {
		if kind == filter.FieldKindUnspecified {
			return coreerrs.Wrapf(filter.ErrInvalidOptions, "field %q has no zero value kind", field)
		}

		if policy.IsArrayField(field) {
			return coreerrs.Wrapf(filter.ErrInvalidOptions, "field %q is declared as both an array and zero-when-absent", field)
		}

		if declared := policy.FieldKind(field); declared != filter.FieldKindUnspecified && declared != kind {
			return coreerrs.Wrapf(filter.ErrInvalidOptions, "field %q has conflicting kinds %s and %s", field, declared, kind)
		}
	}

	for field := range s.presenceFields.Keys() {
		if policy.IsArrayField(field) {
			return coreerrs.Wrapf(filter.ErrInvalidOptions, "field %q is declared as both an array and presence", field)
		}
	}

	return nil
}

// ZeroWhenAbsent returns the declared zero-value kind for a CEL field.
func (s *Schema) ZeroWhenAbsent(field string) (filter.FieldKind, bool) {
	if s == nil {
		return filter.FieldKindUnspecified, false
	}

	return s.zeroWhenAbsent.Get(field)
}

// IsOmitEmpty reports whether a CEL field uses BSON omitempty behavior.
func (s *Schema) IsOmitEmpty(field string) bool {
	return s != nil && s.omitEmpty.Contains(field)
}

// IsPresenceField reports whether strict nullable semantics are enabled for field.
func (s *Schema) IsPresenceField(field string) bool {
	return s != nil && s.presenceFields != nil && s.presenceFields.Contains(field)
}

// HasPresenceFields reports whether strict nullable semantics are configured.
func (s *Schema) HasPresenceFields() bool {
	return s != nil && s.presenceFields != nil && s.presenceFields.Len() != 0
}

// PresenceFields returns the declared presence fields as a set.
func (s *Schema) PresenceFields() map[string]bool {
	fields := make(map[string]bool)

	if s != nil && s.presenceFields != nil {
		for field := range s.presenceFields.Keys() {
			fields[field] = true
		}
	}

	return fields
}

// IsPresenceCall reports whether call targets a configured presence field.
func (s *Schema) IsPresenceCall(call *filter.CallNode) bool {
	if call == nil {
		return false
	}

	ident, ok := call.Target.(*filter.IdentNode)

	return ok && s.IsPresenceField(ident.Name)
}

// hasOmitEmpty reports whether the schema declares any omitted fields.
func (s *Schema) hasOmitEmpty() bool { return s != nil && s.omitEmpty.Len() != 0 }

// omitEmptyPaths returns the mapped storage paths declared with omitempty.
func (s *Schema) mappedOmitEmptyPaths(policy *filter.Policy) []string {
	if !s.hasOmitEmpty() || policy == nil {
		return nil
	}

	paths := make([]string, 0, s.omitEmpty.Len())
	for field := range s.omitEmpty.Keys() {
		paths = append(paths, policy.ApplyFieldMapping(field))
	}

	return paths
}

// BindPolicy returns a copy of the schema with omission paths resolved through
// the given immutable field mapping.
func (s *Schema) BindPolicy(policy *filter.Policy) (*Schema, error) {
	if err := s.ValidatePolicy(policy); err != nil {
		return nil, err
	}

	paths := make(map[string]struct{}, s.omitEmpty.Len())
	for _, path := range s.mappedOmitEmptyPaths(policy) {
		paths[path] = struct{}{}
	}

	bound := *s
	bound.omitEmptyPaths = coremaps.NewImmutableMap(paths)

	return &bound, nil
}

// omitsEmptyAt reports whether a mapped storage path is omitted.
func (s *Schema) omitsEmptyAt(path string) bool {
	return s != nil && s.omitEmptyPaths != nil && s.omitEmptyPaths.Contains(path)
}

// SizeZeroWhenAbsent reports whether size() uses resolved-value semantics for
// fields without a declared missing-value kind.
func (s *Schema) SizeZeroWhenAbsent() bool { return s != nil && s.sizeZeroWhenAbsent }

// MeasuresResolvedSize reports whether size() measures the resolved value of
// an array field, a presence field, or a field governed by WithSchemaSizeZeroWhenAbsent.
func (s *Schema) MeasuresResolvedSize(field string, policy *filter.Policy) bool {
	if policy == nil {
		return false
	}

	if policy.IsArrayField(field) {
		return true
	}

	if s.IsPresenceField(field) {
		return true
	}

	_, zero := s.ZeroWhenAbsent(field)

	return s.SizeZeroWhenAbsent() && !zero
}
