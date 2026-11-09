// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"slices"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/projection"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Projection values for the two modes.
const (
	include int32 = 1
	exclude int32 = 0
)

// ProjectionTranslator converts a [projection.Spec] into a MongoDB projection document
// for an aggregation $project stage or a find projection. It is safe for
// concurrent use after construction.
type ProjectionTranslator struct {
	config   *projection.TranslatorContext
	duration *projection.Telemetry
}

// NewProjectionTranslator creates a MongoDB projection translator bound to policy, the
// client-facing [projection.Policy] shared by every backend of the resource.
// It returns the construction errors of [projection.NewTranslatorContext].
func NewProjectionTranslator(policy *projection.Policy, opts ...ProjectionOption) (*ProjectionTranslator, error) {
	o := newProjectionOptions(opts...)

	storage := o.projectionOptions
	if o.includeID {
		base, err := projection.NewTranslatorContext(policy, storage...)
		if err != nil {
			return nil, err
		}

		required := append(base.RequiredFields(), o.idField)
		storage = append(storage, projection.WithRequiredFields(required...))
	}

	ctx, err := projection.NewTranslatorContext(policy, storage...)
	if err != nil {
		return nil, err
	}

	return &ProjectionTranslator{config: ctx, duration: projection.NewTelemetry(o.collector, "mongo")}, nil
}

// Translate resolves a read mask against the policy and renders it:
//
//   - an inclusion projection {path: 1, …} for a non-empty mask, or for every
//     authorized field under a restrictive allow-list; _id is suppressed
//     ({_id: 0}) unless a selected path covers it, so list
//     WithRequiredFields("_id") to keep it;
//   - an exclusion projection {path: 0, …} for every authorized field under
//     a policy with no allow-list, listing the denied fields;
//   - nil when every field is returned. Callers can pass nil straight to the
//     store/mongo list helpers, which then add no $project stage.
//
// An omitted mask and "*" select every authorized field (AIP-157). Paths
// that an ancestor already covers are dropped after the field mapping is
// applied, because MongoDB rejects "a" and "a.b" in one projection as a path
// collision.
func (t *ProjectionTranslator) Translate(spec projection.Spec) (bson.M, error) {
	defer t.duration.Start()()

	sel, err := t.config.Resolve(spec)
	if err != nil {
		return nil, err
	}

	return render(sel)
}

// TranslateView is [ProjectionTranslator.Translate] for a method with a resource view:
// a present read mask decides the fields, an omitted one defers to view, and
// [projection.ViewUnspecified] to the policy's default view (see
// [projection.Policy.Select]).
func (t *ProjectionTranslator) TranslateView(spec projection.Spec, view projection.View) (bson.M, error) {
	defer t.duration.Start()()

	sel, err := t.config.ResolveView(spec, view)
	if err != nil {
		return nil, err
	}

	return render(sel)
}

// render expresses sel as a MongoDB projection document.
func render(sel projection.Selection) (bson.M, error) {
	if sel.Mode == projection.ModeNone {
		return nil, coreerrs.Wrap(projection.ErrUnsupportedSelection, "a MongoDB projection cannot select no field")
	}

	switch {
	case len(sel.Include) > 0:
		paths := collapse(sel.Include)

		out := make(bson.M, len(paths)+1)
		for _, p := range paths {
			out[p] = include
		}

		if !slices.ContainsFunc(paths, func(p string) bool { return fieldpolicy.Overlaps(p, "_id") }) {
			out["_id"] = exclude
		}

		return out, nil
	case len(sel.Exclude) > 0:
		paths := collapse(sel.Exclude)

		out := make(bson.M, len(paths))
		for _, p := range paths {
			out[p] = exclude
		}

		return out, nil
	default:
		return nil, nil //nolint:nilnil // nil is the documented "every field" projection, not a missing value.
	}
}

// collapse drops every path whose ancestor is also present. paths must be
// sorted and deduplicated: an ancestor then precedes its descendants, though
// not necessarily adjacently ("a", "a-b", "a.b"), so a kept path that
// overlaps p is p's ancestor.
func collapse(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if !slices.ContainsFunc(out, func(kept string) bool { return fieldpolicy.Overlaps(p, kept) }) {
			out = append(out, p)
		}
	}

	return out
}
