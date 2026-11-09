// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection

import (
	"slices"
	"strings"

	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/metrics"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// TranslatorContext is the read-side handle backend translators thread
// through their Translate calls: a [Policy] bound to the storage options of
// one backend. A *TranslatorContext is always the product of a successful
// [NewTranslatorContext]: every policy conflict has been reported at
// construction.
type TranslatorContext = translatorOptions

// NewTranslatorContext binds policy to the storage options of one backend and
// validates the result. It fails with:
//
//   - [ErrPolicyRequired] for a nil policy;
//   - [ErrInvalidFieldPath] / [ErrUnsupportedPath] for a malformed denied or
//     required storage path (checked with [WithStoragePathValidator], never
//     with the policy's API path validator);
//   - [ErrConflictingPolicy] when a required field is denied, or a view field
//     reaches a denied storage path;
//   - [ErrAllFieldsUnresolvable] when the roots of a restrictive allow-list —
//     every authorized field — reach a denied storage path.
//
// The policy is shared, never modified, and may be bound to any number of
// translators. Misconfiguration therefore surfaces at process start rather
// than on the first request.
func NewTranslatorContext(policy *Policy, opts ...TranslatorOption) (*TranslatorContext, error) {
	if policy == nil {
		return nil, ErrPolicyRequired
	}

	o := newTranslatorOptions(opts...)
	o.policy = policy

	if o.storagePathValidator == nil {
		o.storagePathValidator = checkPath
	}

	for _, group := range [][]string{o.deniedStorageFields, o.requiredFields} {
		for _, p := range group {
			if err := o.storagePathValidator(p); err != nil {
				return nil, err
			}
		}
	}

	o.deniedStorage = slices.Clone(o.deniedStorageFields)
	for _, d := range policy.deniedFields {
		o.deniedStorage = append(o.deniedStorage, o.fieldMapping.Reach(d)...)
	}

	o.deniedStorage = sortedSet(o.deniedStorage)

	for _, r := range o.requiredFields {
		if d, ok := overlapping(r, o.deniedStorage); ok {
			return nil, coreerrs.Wrapf(ErrConflictingPolicy, "required field %q overlaps denied field %q", r, d)
		}
	}

	o.viewSelections = make(map[View]Selection, len(policy.viewSet))
	for view, fields := range policy.viewSet {
		sel, err := o.selection(fields)
		if err != nil {
			return nil, err
		}

		o.viewSelections[view] = sel
	}

	return o, nil
}

// Telemetry is the translation timer of one projection translator, created by [NewTelemetry]. A nil *Telemetry is
// disabled.
type Telemetry = metrics.TranslateTimer

// NewTelemetry creates the translation timer of a projection translator, recorded as the histogram
// projection_<backend>_translate_duration_seconds. Translators create it from the collector of their
// WithProjectionCollector option. A nil meter disables collection and returns nil, whose Start is a no-op.
func NewTelemetry(m metrics.Meter, backend string) *Telemetry {
	return metrics.NewTranslateTimer(m, "projection_"+backend, "Duration of projection translation in seconds.")
}

// Policy returns the client-facing policy the context is bound to.
func (o *translatorOptions) Policy() *Policy { return o.policy }

// ViewSelection returns the projection of view, resolved at construction:
// every authorized field for [ViewFull], the configured paths of a view set
// with [WithView] and the default view for [ViewUnspecified], plus the
// required fields. Another view fails with [ErrInvalidView]. Backends that
// cannot render an exclusion check every view of [Policy.Views] with it at
// construction.
func (o *translatorOptions) ViewSelection(view View) (Selection, error) {
	v, err := o.policy.resolveView(view)
	if err != nil {
		return Selection{}, err
	}

	return o.viewSelections[v].clone(), nil
}

// DeclaresField reports whether the service named the API path explicitly: an exact [WithAllowedFields] entry of the policy
// ([Policy.DeclaresField]) or an exact [WithFieldMapping] entry. A path reached only through a wildcard allow-list entry or a
// [WithFieldPrefixMapping] rule is not declared; an SQL translator refuses such a dotted path, whose dot would name a table.
func (o *translatorOptions) DeclaresField(path string) bool {
	return o.policy.DeclaresField(path) || o.fieldMapping.HasExact(path)
}

// RequiredFields returns an independent snapshot of required storage paths.
func (o *translatorOptions) RequiredFields() []string { return slices.Clone(o.requiredFields) }

// Resolve validates a read mask with the bound [Policy] and returns the
// projection in storage names: the mapped subtrees of the authorized paths,
// checked against the storage deny-list, plus the required fields. An
// omitted mask and "*" resolve to every authorized field, as
// [Policy.Validate] does. A translator therefore never renders a path its
// policy refuses.
func (o *translatorOptions) Resolve(spec Spec) (Selection, error) {
	if spec.IsEmpty() || (spec.Mode == ModeAll && len(spec.Paths) == 0) {
		return o.viewSelections[ViewFull].clone(), nil
	}

	fields, err := o.policy.Validate(spec)
	if err != nil {
		return Selection{}, err
	}

	return o.selection(fields)
}

// ResolveView applies the selection rule of [Policy.Select] and returns the
// projection in storage names: a present read mask decides as in
// [translatorOptions.Resolve], an omitted one defers to view, and
// [ViewUnspecified] to the policy's default view.
func (o *translatorOptions) ResolveView(spec Spec, view View) (Selection, error) {
	sel, err := o.ViewSelection(view)
	if err != nil {
		return Selection{}, err
	}

	if !spec.IsEmpty() {
		return o.Resolve(spec)
	}

	return sel, nil
}

// selection expresses authorized fields in storage names.
func (o *translatorOptions) selection(f Fields) (Selection, error) {
	switch f.mode {
	case ModeNone:
		if len(o.requiredFields) > 0 {
			return Selection{Include: slices.Clone(o.requiredFields)}, nil
		}

		return Selection{Mode: ModeNone}, nil
	case ModeAll:
		return Selection{Exclude: slices.Clone(o.deniedStorage)}, nil
	case ModeDefault:
	}

	include := make([]string, 0, len(f.paths)+len(o.requiredFields))
	for _, p := range f.paths {
		var err error
		if include, err = o.appendStorageNames(include, p, f.origin == originAll); err != nil {
			switch f.origin {
			case originView:
				return Selection{}, coreerrs.Wrapf(ErrConflictingPolicy, "view field %q: %w", p, err)
			case originAll:
				return Selection{}, coreerrs.Wrapf(ErrAllFieldsUnresolvable, "allow-list root %q: %w", p, err)
			default:
				return Selection{}, err
			}
		}
	}

	if f.origin == originAll && len(include) == 0 {
		return Selection{}, coreerrs.Wrapf(ErrAllFieldsUnresolvable, "every allow-list root reaches a denied storage path")
	}

	include = append(include, o.requiredFields...)

	return Selection{Include: sortedSet(include)}, nil
}

// appendStorageNames appends to dst the storage paths that hold the subtree
// of the authorized API path p (see [fieldpolicy.Mapping.Subtree]) and
// rejects one that overlaps a denied storage path. With skipDenied, used for
// every authorized field, a storage path inside a denied one is dropped
// instead: it is simply not authorized. A path carrying a denied descendant
// is rejected either way.
func (o *translatorOptions) appendStorageNames(dst []string, p string, skipDenied bool) ([]string, error) {
	start := len(dst)

	dst = o.fieldMapping.AppendSubtree(dst, p)
	kept := dst[:start]

	for _, s := range dst[start:] {
		if skipDenied && covered(s, o.deniedStorage) {
			continue
		}

		if d, ok := overlapping(s, o.deniedStorage); ok {
			return nil, coreerrs.Wrapf(ErrFieldNotAllowed, "%s overlaps denied field %s", p, d)
		}

		kept = append(kept, s)
	}

	return kept, nil
}

// covered reports whether path equals an entry of set or lies under one.
func covered(path string, set []string) bool {
	for _, d := range set {
		if path == d || strings.HasPrefix(path, d+".") {
			return true
		}
	}

	return false
}

// overlapping returns the first entry of set that overlaps path.
func overlapping(path string, set []string) (string, bool) {
	for _, d := range set {
		if fieldpolicy.Overlaps(path, d) {
			return d, true
		}
	}

	return "", false
}

// sortedSet sorts s in place and drops duplicates.
func sortedSet(s []string) []string {
	slices.Sort(s)
	return slices.Compact(s)
}

// PathValidator validates an API or configured policy path. It must be safe for
// concurrent use. Backend resolvers must still quote or encode storage paths.
type PathValidator func(string) error
