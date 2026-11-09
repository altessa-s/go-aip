// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package orderby

import (
	"slices"

	"github.com/altessa-s/go-aip/fieldpolicy"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Policy is the immutable, backend-independent field policy of one resource's order_by: the allow-list, the shared field access
// policy, denied API fields, the default order and the untrusted-input guard, all in API names. Build it once with [NewPolicy]
// and share it between the translators of every backend that serves the resource. Its zero value is not a configured policy.
type Policy struct {
	policyOptions
}

// NewPolicy builds and validates a [Policy]. It fails with:
//
//   - an error wrapping [fieldpolicy.ErrMalformedPattern] for a malformed [WithAllowedFields] entry or entry of a
//     [fieldpolicy.AllowList] given to [WithFieldAccess]: the entry matches nothing, which is almost always a typo;
//   - [ErrAllowlistRequired] when [WithUntrustedInput] is set without an allow-list that restricts the fields — that
//     combination would otherwise accept a sort on any field, or silently reject every sort (see below);
//   - the error [Policy.Validate] would return for a default order key the policy itself refuses.
//
// Under WithUntrustedInput an allow-list restricts the fields when it is configured, holds an entry and does not match
// every field: a non-empty WithAllowedFields list without a lone "*", or such a [fieldpolicy.AllowList] given to
// WithFieldAccess. The zero AllowList and one holding "*" allow every field, and an opaque [fieldpolicy.Access], whose reach
// cannot be inspected, must be paired with a restrictive WithAllowedFields list, as projection and filter require.
// Misconfiguration therefore surfaces at process start rather than on the first request.
func NewPolicy(opts ...PolicyOption) (*Policy, error) {
	p := &Policy{policyOptions: *newPolicyOptions(opts...)}
	if err := p.allowedFields.Validate(); err != nil {
		return nil, coreerrs.Wrap(err, "allowed fields")
	}

	access, isList := fieldpolicy.AsAllowList(p.fieldAccess)
	if isList {
		if err := access.Validate(); err != nil {
			return nil, coreerrs.Wrap(err, "field access")
		}
	}

	if p.untrustedInput && !p.allowedFields.Restricts() && (!isList || !access.Restricts()) {
		return nil, ErrAllowlistRequired
	}

	if _, err := p.checkKeys(p.defaultOrder); err != nil {
		return nil, coreerrs.Wrap(err, "default order")
	}

	return p, nil
}

// DeclaresField reports whether the service named the API field explicitly as an exact [WithAllowedFields] entry. A field the
// policy admits only through a wildcard ("address.*", "*") or with no allow-list at all is not declared. A translator whose
// storage reads a dot as structure rather than nesting — an SQL dialect, where "a.b" is table.column — uses it, through
// [TranslatorContext.DeclaresField], to refuse a dotted name the client composed itself.
func (p *Policy) DeclaresField(field string) bool { return p.allowedFields.HasExact(field) }

// IsFieldAllowed reports whether a client may sort on the API field: the allow-list (all fields when none is configured) and the
// field access policy admit it and it overlaps no denied field.
func (p *Policy) IsFieldAllowed(field string) bool {
	if !p.allowedFields.Allows(field) || (p.fieldAccess != nil && !p.fieldAccess.Allows(field)) {
		return false
	}

	for _, d := range p.deniedFields {
		if fieldpolicy.Overlaps(field, d) {
			return false
		}
	}

	return true
}

// DeniedFields returns an independent snapshot of the denied API paths.
func (p *Policy) DeniedFields() []string { return slices.Clone(p.deniedFields) }

// Validate checks spec against the policy and returns it normalized: an empty spec takes the default order, and every key must
// name an allowed field with a known direction and null placement. Errors wrap [ErrFieldNotAllowed], [ErrInvalidDirection] or
// [ErrUnsupportedOrdering] and map to INVALID_ARGUMENT. Keys stay in API names; spec is never modified, and validating the result
// again returns it unchanged.
func (p *Policy) Validate(spec Spec) (Spec, error) {
	keys := spec.Keys
	if len(keys) == 0 {
		keys = p.defaultOrder
	}

	return p.checkKeys(keys)
}

// checkKeys authorizes keys and returns an independent copy of them.
func (p *Policy) checkKeys(keys []Key) (Spec, error) {
	for _, k := range keys {
		if k.Name == "" {
			return Spec{}, ErrInvalidFieldPath
		}

		if !p.IsFieldAllowed(k.Name) {
			return Spec{}, coreerrs.Wrapf(ErrFieldNotAllowed, "%s", k.Name)
		}

		if err := checkAttributes(k); err != nil {
			return Spec{}, err
		}
	}

	return Spec{Keys: slices.Clone(keys)}, nil
}

// checkAttributes rejects a direction or null placement outside the defined values.
func checkAttributes(k Key) error {
	if k.Direction != DirectionAscending && k.Direction != DirectionDescending {
		return ErrInvalidDirection
	}

	if k.Nulls > NullsLast {
		return ErrUnsupportedOrdering
	}

	return nil
}
