// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing

import (
	"fmt"
	"slices"
	"time"

	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/filter"
)

// defaultEquality is a validated [WithDefaultEqualities] restriction.
type defaultEquality struct {
	field string
	value any
}

// compileDefaults validates the [WithDefaultEqualities] restrictions: each
// names a declared scalar field and a literal of its kind (an enum field
// takes an enum name).
func (s *Schema) compileDefaults() error {
	for _, name := range slices.Sorted(s.opts.defaultEqualities.Keys()) {
		f, ok := s.fields.Get(name)
		if !ok {
			return invalid("default equality: field %q is not declared", name)
		}

		input, _ := s.opts.defaultEqualities.Get(name)

		value, err := s.opts.defaultLiteral(s.fieldMetadata(f), input)
		if t, ok := value.(time.Time); ok {
			value = s.storedTime(t)
		}

		if err != nil {
			return fmt.Errorf("%w: default equality on %q: %w", ErrInvalidSchema, name, err)
		}

		if err := s.policy.CheckLiteralKind(name, &filter.LiteralNode{Value: value}); err != nil {
			return fmt.Errorf("%w: default equality on %q: %w", ErrInvalidSchema, name, err)
		}

		s.defaults = append(s.defaults, defaultEquality{field: name, value: value})
	}

	return nil
}

// defaultLiteral converts v to the filter literal of field f: for an enum field
// the stored value of the enum name v, else a value of the field's kind
// (integers as int64). The profile normalizes timestamp results. Defaults join the
// AST after parsing, so they carry stored values, as parsed enum names do.
func defaultLiteral(f Field, v any) (any, error) {
	if f.Array {
		return nil, fmt.Errorf("field %q is an array", f.Name)
	}

	if f.Enum != nil {
		name, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("an enum field takes an enum name, not %T", v)
		}

		stored, known := f.Enum[name]
		if !known {
			return nil, fmt.Errorf("unknown enum name %q", name)
		}

		return stored, nil
	}

	switch f.Kind {
	case filter.FieldKindString:
		if s, ok := v.(string); ok {
			return s, nil
		}
	case filter.FieldKindBool:
		if b, ok := v.(bool); ok {
			return b, nil
		}
	case filter.FieldKindInt:
		if n, ok := fieldpolicy.IntegerValue(v); ok {
			return n, nil
		}
	case filter.FieldKindFloat:
		if x, ok := v.(float64); ok {
			return x, nil
		}
	case filter.FieldKindDecimal:
		return v, nil
	case filter.FieldKindBytes:
		if b, ok := v.([]byte); ok {
			return slices.Clone(b), nil
		}
	case filter.FieldKindTimestamp:
		if t, ok := v.(time.Time); ok {
			return t, nil
		}
	default:
		return v, nil // Custom kinds are validated by the schema policy.
	}

	return nil, fmt.Errorf("value of type %T does not match kind %s", v, f.Kind)
}
