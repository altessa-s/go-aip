// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection

import (
	"maps"
	"slices"

	"github.com/altessa-s/go-aip/fieldpolicy"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Policy is the immutable, backend-independent field policy of one resource's projection: the allow-list, the shared field access
// policy, denied API fields, the resource views, the path grammar and the untrusted-input guard, all in API names. Build it once
// with [NewPolicy] and share it between the translators of every backend that serves the resource. Its zero value is not a
// configured policy.
type Policy struct {
	policyOptions
	// all is every authorized field: the selection of an omitted read mask, of "*" and of ViewFull. Resolved by NewPolicy.
	all Fields
	// viewSet holds the authorized fields of each configured view. Resolved by NewPolicy.
	viewSet map[View]Fields
}

// origin records where the paths of [Fields] come from; it selects the sentinel of a storage-level failure.
type origin uint8

const (
	// originRequest marks paths the client listed: a failure is [ErrFieldNotAllowed].
	originRequest origin = iota
	// originView marks the paths of a view configured with [WithView]: a failure is [ErrConflictingPolicy].
	originView
	// originAll marks every authorized field, derived from the policy: a failure is [ErrAllFieldsUnresolvable].
	originAll
)

// Fields is a projection request authorized by a [Policy], in API names. Its paths are what a translator maps to storage names;
// a Fields with [ModeAll] and no paths selects every field except the denied ones, and one with [ModeNone] selects none.
type Fields struct {
	paths  []string
	mode   Mode
	origin origin
}

// Mode returns [ModeAll] for "every field except the denied ones", [ModeNone] for an empty selection, and [ModeDefault] for the
// listed paths.
func (f Fields) Mode() Mode { return f.mode }

// Paths returns an independent copy of the authorized API paths.
func (f Fields) Paths() []string { return slices.Clone(f.paths) }

// NewPolicy builds and validates a [Policy]. It fails with:
//
//   - an error wrapping [fieldpolicy.ErrMalformedPattern] for a malformed [WithAllowedFields] entry or entry of a
//     [fieldpolicy.AllowList] given to [WithFieldAccess]: the entry matches nothing, which is almost always a typo;
//   - [ErrAllowlistRequired] when [WithUntrustedInput] is set without an allow-list that admits some but not every field:
//     a non-empty WithAllowedFields list without a lone "*", or such a [fieldpolicy.AllowList] given to WithFieldAccess.
//     The zero AllowList and one holding "*" allow every field, and an opaque [fieldpolicy.Access] needs a restrictive
//     WithAllowedFields list beside it, as in orderby and filter;
//   - [ErrInvalidFieldPath] / [ErrUnsupportedPath] for a malformed denied or view path;
//   - [ErrAllFieldsUnresolvable] when every authorized field — what an omitted read mask returns — cannot be enumerated: the
//     allow-list is empty or has embedded wildcards, one of its roots overlaps a denied field or is refused by the field access
//     policy, or a field access policy has no allow-list;
//   - [ErrInvalidView] for a [WithView] of [ViewUnspecified], [ViewFull], a negative view or no path, and for a
//     [WithDefaultView] that is neither [ViewFull] nor configured;
//   - [ErrConflictingPolicy] when a view field is not allowed or overlaps a denied field.
//
// Misconfiguration therefore surfaces at process start rather than on the first request.
func NewPolicy(opts ...PolicyOption) (*Policy, error) {
	p := &Policy{policyOptions: *newPolicyOptions(opts...)}
	if p.pathValidator == nil {
		p.pathValidator = checkPath
	}

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

	for _, path := range p.deniedFields {
		if err := p.pathValidator(path); err != nil {
			return nil, err
		}
	}

	all, err := p.resolveAll()
	if err != nil {
		return nil, err
	}

	p.all = all

	if err := p.resolveViews(); err != nil {
		return nil, err
	}

	if _, err := p.resolveView(p.defaultView); err != nil {
		return nil, coreerrs.Wrapf(ErrInvalidView, "default view %v: %w", p.defaultView, err)
	}

	return p, nil
}

// DeclaresField reports whether the service named the API path explicitly as an exact [WithAllowedFields] entry. A path the
// policy admits only through a wildcard ("address.*", "*") or with no allow-list at all is not declared. An SQL translator,
// whose storage reads a dot as table.column, uses it through [TranslatorContext.DeclaresField] to refuse a dotted path the
// client composed itself.
func (p *Policy) DeclaresField(path string) bool { return p.allowedFields.HasExact(path) }

// DeniedFields returns an independent snapshot of the denied API paths.
func (p *Policy) DeniedFields() []string { return slices.Clone(p.deniedFields) }

// DefaultView returns the view an unspecified request selects.
func (p *Policy) DefaultView() View { return p.defaultView }

// Views returns the views the policy serves, in ascending order: [ViewFull] and every view configured with [WithView].
func (p *Policy) Views() []View {
	return slices.Sorted(maps.Keys(p.viewSet))
}

// Validate checks a read mask against the policy and returns the authorized request in API names. An omitted mask (an empty
// spec) and ModeAll ("*") both return every authorized field (AIP-157: "if the field mask is not provided, all fields must be
// returned"), and every listed path must pass the grammar, the allow-list, the field access policy and the deny-list. Validate
// ignores views: a method with a view parameter calls [Policy.Select]. Errors wrap [ErrInvalidFieldPath], [ErrUnsupportedPath]
// or [ErrFieldNotAllowed] and map to INVALID_ARGUMENT. Paths are re-validated against the grammar, so a hand-built Spec cannot
// smuggle an operator, an index or an empty segment into a query.
func (p *Policy) Validate(spec Spec) (Fields, error) {
	if spec.Mode > ModeNone || (spec.Mode != ModeDefault && len(spec.Paths) > 0) {
		return Fields{}, ErrInvalidFieldPath
	}

	switch {
	case spec.Mode == ModeNone:
		return Fields{mode: ModeNone}, nil
	case spec.Mode == ModeAll, spec.IsEmpty():
		return p.all.clone(), nil
	}

	for _, path := range spec.Paths {
		if err := p.check(path); err != nil {
			return Fields{}, err
		}
	}

	return Fields{paths: slices.Clone(spec.Paths), origin: originRequest}, nil
}

// Select applies the AIP-157 selection rule to a request carrying a read mask and a view. A present mask (paths, or "*")
// decides the fields as in [Policy.Validate]; an omitted one defers to view, and [ViewUnspecified] to the default view
// ([WithDefaultView]). The view must be [ViewUnspecified], [ViewFull] or configured even when the mask decides, so an unknown
// enum value is always reported. Errors map to INVALID_ARGUMENT: those of Validate, and [ErrInvalidView] for an unknown view.
func (p *Policy) Select(spec Spec, view View) (Fields, error) {
	vf, err := p.ViewFields(view)
	if err != nil {
		return Fields{}, err
	}

	if !spec.IsEmpty() {
		return p.Validate(spec)
	}

	return vf, nil
}

// ViewFields returns the authorized fields of view: every authorized field for [ViewFull], the configured paths of a view set
// with [WithView], and the fields of the default view for [ViewUnspecified]. Another view fails with [ErrInvalidView].
func (p *Policy) ViewFields(view View) (Fields, error) {
	v, err := p.resolveView(view)
	if err != nil {
		return Fields{}, err
	}

	return p.viewSet[v].clone(), nil
}

// resolveView replaces ViewUnspecified with the default view and rejects a view the policy does not serve.
func (p *Policy) resolveView(view View) (View, error) {
	view = view.Or(p.defaultView)
	if _, ok := p.viewSet[view]; !ok {
		return 0, coreerrs.Wrapf(ErrInvalidView, "%v is not served", view)
	}

	return view, nil
}

// check validates one API path against the grammar, the allow-list, the field access policy and the deny-list.
func (p *Policy) check(path string) error {
	if err := p.pathValidator(path); err != nil {
		return err
	}

	if !p.allows(path) {
		return coreerrs.Wrapf(ErrFieldNotAllowed, "%s", path)
	}

	if d, ok := overlapping(path, p.deniedFields); ok {
		return coreerrs.Wrapf(ErrFieldNotAllowed, "%s overlaps denied field %s", path, d)
	}

	return nil
}

// allows reports whether the allow-list and the field access policy admit path.
func (p *Policy) allows(path string) bool {
	return p.allowedFields.Allows(path) && (p.fieldAccess == nil || p.fieldAccess.Allows(path))
}

// resolveViews validates the configured views and records them, with ViewFull, in viewSet.
func (p *Policy) resolveViews() error {
	p.viewSet = make(map[View]Fields, len(p.views)+1)
	p.viewSet[ViewFull] = p.all

	for view, paths := range p.views {
		if view <= ViewUnspecified || view == ViewFull {
			return coreerrs.Wrapf(ErrInvalidView, "%v cannot be configured", view)
		}

		if len(paths) == 0 {
			return coreerrs.Wrapf(ErrInvalidView, "%v lists no field", view)
		}

		for _, path := range paths {
			if err := p.check(path); err != nil {
				return coreerrs.Wrapf(ErrConflictingPolicy, "view %v: %w", view, err)
			}
		}

		paths = slices.Clone(paths)
		slices.Sort(paths)
		p.viewSet[view] = Fields{paths: slices.Compact(paths), origin: originView}
	}

	return nil
}

// resolveAll derives every authorized field: the roots of a restrictive allow-list, or every field except the denied ones.
func (p *Policy) resolveAll() (Fields, error) {
	switch {
	case !p.allowedFields.MatchesAll():
		roots := p.allowedFields.Roots()
		if len(roots) == 0 {
			return Fields{}, coreerrs.Wrapf(ErrAllFieldsUnresolvable, "the allow-list is empty or has embedded wildcards")
		}

		kept := roots[:0]
		for _, path := range roots {
			if p.fieldAccess != nil && !p.fieldAccess.Allows(path) {
				return Fields{}, coreerrs.Wrapf(ErrAllFieldsUnresolvable, "allow-list root %q is refused by the field access policy", path)
			}

			// A root inside a denied field adds nothing; one that carries a denied descendant cannot be selected whole.
			if covered(path, p.deniedFields) {
				continue
			}

			if d, ok := overlapping(path, p.deniedFields); ok {
				return Fields{}, coreerrs.Wrapf(ErrAllFieldsUnresolvable, "allow-list root %q: %w", path,
					coreerrs.Wrapf(ErrFieldNotAllowed, "%s overlaps denied field %s", path, d))
			}

			kept = append(kept, path)
		}

		if len(kept) == 0 {
			return Fields{}, coreerrs.Wrapf(ErrAllFieldsUnresolvable, "every allow-list root is denied")
		}

		return Fields{paths: kept, origin: originAll}, nil
	case p.fieldAccess != nil:
		return Fields{}, coreerrs.Wrapf(ErrAllFieldsUnresolvable, "a field access policy needs an allow-list to enumerate the fields")
	default:
		// No allow-list: everything except what is denied.
		return Fields{mode: ModeAll, origin: originAll}, nil
	}
}

// clone returns a copy of f whose paths are independent of the original.
func (f Fields) clone() Fields {
	f.paths = slices.Clone(f.paths)
	return f
}
