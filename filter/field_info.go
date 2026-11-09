// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import (
	"iter"
	"slices"

	coremaps "github.com/altessa-s/go-atlas-core/collections/maps"
)

// FieldPolicy is what the options declare about one CEL-side field. [NewPolicy] resolves it once for every field an
// option names, so the evaluator reads a field's options with one lookup instead of one per option.
type FieldPolicy struct {
	codec   ValueCodec
	policy  *Policy
	name    string
	path    string     // the mapped data path ([WithFieldMapping])
	enum    *fieldEnum // the allowed values ([WithEnumValues]); nil without a declaration
	kind    FieldKind  // the declared kind ([WithFieldTypes])
	allowed bool       // the allow-list admits the field ([WithAllowedFields])
	array   bool       // declared by [WithArrayFields]
}

// fieldEnum is the value set [WithEnumValues] declares for a field.
type fieldEnum struct {
	set     *coremaps.ImmutableMap[int64, struct{}]
	members []int64 // the values of set, sorted: a lookup cheaper than set's
}

// Field returns an immutable value describing name under this policy. Undeclared
// fields resolve mappings and authorization through the same policy as declared fields.
func (o *Policy) Field(name string) FieldPolicy {
	if o.fields != nil {
		if f, ok := o.fields.Get(name); ok {
			return f
		}
	}

	return o.resolveField(name)
}

// Allowed reports whether the policy that produced f permits its field.
func (f FieldPolicy) Allowed() bool { return f.allowed }

// resolveField reads what the options declare about the CEL-side field name, one option at a time.
func (o *Policy) resolveField(name string) FieldPolicy {
	var enum *fieldEnum
	if set, ok := o.EnumValues(name); ok {
		enum = &fieldEnum{set: set, members: slices.Sorted(set.Keys())}
	}

	return FieldPolicy{
		name:    name,
		codec:   o.valueCodec(o.FieldKind(name)),
		policy:  o,
		path:    o.ApplyFieldMapping(name),
		enum:    enum,
		kind:    o.FieldKind(name),
		allowed: o.IsFieldAllowed(name),
		array:   o.IsArrayField(name),
	}
}

// resolveFields resolves [FieldPolicy] for every field an option names.
func (o *Policy) resolveFields() {
	fields := map[string]FieldPolicy{}

	add := func(names iter.Seq[string]) {
		for name := range names {
			fields[name] = o.resolveField(name)
		}
	}
	if o.allowedFields != nil {
		add(o.allowedFields.Keys())
	}

	if o.fieldMapping != nil {
		add(o.fieldMapping.Keys())
	}

	if o.fieldTypes != nil {
		add(o.fieldTypes.Keys())
	}

	if o.enumValues != nil {
		add(o.enumValues.Keys())
	}

	if o.arrayFields != nil {
		add(o.arrayFields.Keys())
	}

	o.fields = coremaps.NewImmutableMap(fields)
}

// CheckLiteral validates lit against the kind and enum values of the field that
// produced f. Errors name that same field.
func (f FieldPolicy) CheckLiteral(lit *LiteralNode) error {
	if lit.Value != nil {
		if f.codec != nil {
			if err := f.codec.ValidateLiteral(lit.Value); err != nil {
				return err
			}
		} else if f.kind == FieldKindDecimal {
			if _, err := f.policy.DecimalOperand(lit, lit.Value); err != nil {
				return err
			}
		} else if err := checkLiteralFieldKind(f.name, f.kind, lit); err != nil {
			return err
		}
	}

	if f.enum == nil {
		return nil
	}

	if v, ok := lit.Value.(int64); ok {
		if _, found := slices.BinarySearch(f.enum.members, v); found {
			return nil
		}
	}

	return checkEnumMembership(f.name, f.enum.set, lit)
}

// Path returns the mapped data path.
func (f FieldPolicy) Path() string { return f.path }

// IsArray reports whether the field is declared as an array.
func (f FieldPolicy) IsArray() bool { return f.array }
