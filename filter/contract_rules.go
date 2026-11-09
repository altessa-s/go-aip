// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

// ContractRule identifies one independently configurable validation rule.
type ContractRule string

const (
	// LiteralComparisons restricts comparisons to literal operands instead of field-to-field comparisons.
	LiteralComparisons ContractRule = "literal-comparisons"
	// DeclaredFields requires an explicit kind for each referenced field when enabled.
	DeclaredFields ContractRule = "declared-fields"
	// ArrayPresence restricts has/exists to declared arrays when enabled.
	ArrayPresence ContractRule = "array-presence"

	// ScalarPredicates requires bare identifiers to name scalar bool fields.
	ScalarPredicates ContractRule = "scalar-predicates"
	// FiniteFloats rejects non-finite floating-point literals.
	FiniteFloats ContractRule = "finite-floats"
	// OrderedNull rejects null in ordered comparisons.
	OrderedNull ContractRule = "ordered-null"
	// NonemptyIn requires a nonempty membership list.
	NonemptyIn ContractRule = "nonempty-in"
	// NonnullIn rejects null elements of membership lists.
	NonnullIn ContractRule = "nonnull-in"
	// PortableRegex restricts regex syntax and case folding to the portable subset.
	PortableRegex ContractRule = "portable-regex"
)

// ContractRuleEnabled resolves an explicit override before the portable preset.
func (o *Policy) ContractRuleEnabled(rule ContractRule) bool {
	if o.contractRules != nil {
		if enabled, ok := o.contractRules.Get(rule); ok {
			return enabled
		}
	}

	if rule == LiteralComparisons {
		return true
	}

	if rule == DeclaredFields || rule == ArrayPresence {
		return false
	}

	return o.portableContract
}

// ValueCodec validates a literal for an application-defined FieldKind.
// Values must have a deterministic representation and be supported by the selected backend.
type ValueCodec interface{ ValidateLiteral(any) error }

func (o *Policy) valueCodec(kind FieldKind) ValueCodec {
	if o.valueCodecs == nil {
		return nil
	}

	codec, _ := o.valueCodecs.Get(kind)

	return codec
}
