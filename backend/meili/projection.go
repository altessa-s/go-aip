// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili

import (
	"slices"

	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/projection"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// ProjectionTranslator converts a [projection.Spec] into the attributesToRetrieve
// parameter of a Meilisearch search. It is safe for concurrent use after
// construction.
type ProjectionTranslator struct {
	config   *projection.TranslatorContext
	duration *projection.Telemetry
}

// NewProjectionTranslator creates a Meilisearch projection translator bound to policy,
// the client-facing [projection.Policy] shared by every backend of the
// resource. Storage options go through [WithProjectionOptions]. Beyond the
// construction errors of [projection.NewTranslatorContext], it fails with:
//
//   - [projection.ErrAllFieldsUnresolvable] when every authorized field —
//     the projection of an omitted read mask, "*" and FULL — would have to
//     exclude denied fields, which attributesToRetrieve cannot express: list
//     the allowed fields with [projection.WithAllowedFields];
//   - [projection.ErrInvalidFieldPath] when a view maps to a name that is not
//     an attribute.
func NewProjectionTranslator(policy *projection.Policy, opts ...ProjectionOption) (*ProjectionTranslator, error) {
	o := newProjectionOptions(opts...)

	ctx, err := projection.NewTranslatorContext(policy, o.projectionOptions...)
	if err != nil {
		return nil, err
	}

	for _, view := range policy.Views() {
		sel, err := ctx.ViewSelection(view)
		if err != nil {
			return nil, err
		}

		if len(sel.Exclude) > 0 {
			return nil, coreerrs.Wrapf(projection.ErrAllFieldsUnresolvable,
				"attributesToRetrieve cannot exclude denied fields %v", sel.Exclude)
		}

		if _, err := renderProjection(sel); err != nil {
			return nil, err
		}
	}

	return &ProjectionTranslator{config: ctx, duration: projection.NewTelemetry(o.collector, "meili")}, nil
}

// Translate resolves a read mask against the policy and renders the
// attributesToRetrieve list: the selected attributes, required fields
// included, in lexical order, with paths an ancestor covers dropped; nil
// when every attribute is returned, so the parameter is omitted (Meilisearch
// then returns every displayed attribute, as ["*"]). An omitted mask and "*"
// select every authorized field (AIP-157). Dotted names address nested
// attributes.
//
// A ModeNone selection without required fields fails with
// [projection.ErrUnsupportedSelection]: Meilisearch always returns a hit
// object, and the client libraries drop an empty list (omitempty), which
// would return every attribute. Keep the primary key with
// [projection.WithRequiredFields] to select it alone.
func (t *ProjectionTranslator) Translate(spec projection.Spec) ([]string, error) {
	defer t.duration.Start()()

	sel, err := t.config.Resolve(spec)
	if err != nil {
		return nil, err
	}

	return renderProjection(sel)
}

// TranslateView is [ProjectionTranslator.Translate] for a method with a resource view:
// a present read mask decides the attributes, an omitted one defers to
// view, and [projection.ViewUnspecified] to the policy's default view (see
// [projection.Policy.Select]).
func (t *ProjectionTranslator) TranslateView(spec projection.Spec, view projection.View) ([]string, error) {
	defer t.duration.Start()()

	sel, err := t.config.ResolveView(spec, view)
	if err != nil {
		return nil, err
	}

	return renderProjection(sel)
}

// renderProjection expresses sel as an attributesToRetrieve list.
func renderProjection(sel projection.Selection) ([]string, error) {
	switch {
	case sel.Mode == projection.ModeNone:
		return nil, coreerrs.Wrap(projection.ErrUnsupportedSelection, "attributesToRetrieve cannot select no attribute")
	case len(sel.Exclude) > 0:
		return nil, coreerrs.Wrap(projection.ErrUnsupportedSelection, "attributesToRetrieve cannot exclude attributes")
	case len(sel.Include) == 0:
		return nil, nil
	}

	out := make([]string, 0, len(sel.Include))

	for _, name := range sel.Include {
		if err := checkName(name); err != nil {
			return nil, err
		}

		if !slices.ContainsFunc(out, func(kept string) bool { return fieldpolicy.Overlaps(name, kept) }) {
			out = append(out, name)
		}
	}

	return out, nil
}

// checkName rejects a storage name that is not one attribute: one that is not
// one or more dot-separated segments of ASCII letters, digits, `_` and `-`,
// which also refuses the wildcard "*" that selects every attribute.
func checkName(name string) error {
	if !validAttribute(name) {
		return coreerrs.Wrapf(projection.ErrInvalidFieldPath, "%q is not a Meilisearch attribute", name)
	}

	return nil
}
