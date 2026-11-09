// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/altessa-s/go-aip/projection"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Return is the keyword of the FT.SEARCH clause the translator renders.
const Return = "RETURN"

// ProjectionTranslator converts a [projection.Spec] into the RETURN clause of a Redis
// Query Engine (RediSearch) FT.SEARCH command. It is safe for concurrent use
// after construction.
type ProjectionTranslator struct {
	config   *projection.TranslatorContext
	duration *projection.Telemetry
}

// NewProjectionTranslator creates a RediSearch projection translator bound to policy,
// the client-facing [projection.Policy] shared by every backend of the
// resource. Storage options go through [WithProjectionOptions]. Beyond the
// construction errors of [projection.NewTranslatorContext], it fails with:
//
//   - [projection.ErrAllFieldsUnresolvable] when every authorized field —
//     the projection of an omitted read mask, "*" and FULL — would have to
//     exclude denied fields, which RETURN cannot express: list the allowed
//     fields with [projection.WithAllowedFields];
//   - [projection.ErrInvalidFieldPath] when a view maps to a name RETURN
//     cannot carry.
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
				"RETURN cannot exclude denied fields %v", sel.Exclude)
		}

		if _, err := renderReturn(sel); err != nil {
			return nil, err
		}
	}

	return &ProjectionTranslator{config: ctx, duration: projection.NewTelemetry(o.collector, "redis")}, nil
}

// Translate resolves a read mask against the policy and renders the
// arguments to append to FT.SEARCH:
//
//   - RETURN n field … for a field list, required fields included, in
//     lexical order;
//   - RETURN 0 for a ModeNone selection without required fields: document
//     keys only, as NOCONTENT;
//   - nil when every field is returned: append no RETURN clause.
//
// An omitted mask and "*" select every authorized field (AIP-157). Storage
// names are attribute identifiers or, on a JSON index, JSONPath expressions
// such as "$.address.city" reached through the field mapping; they are passed
// as separate arguments and never aliased.
func (t *ProjectionTranslator) Translate(spec projection.Spec) ([]string, error) {
	defer t.duration.Start()()

	sel, err := t.config.Resolve(spec)
	if err != nil {
		return nil, err
	}

	return renderReturn(sel)
}

// TranslateView is [ProjectionTranslator.Translate] for a method with a resource view:
// a present read mask decides the fields, an omitted one defers to view, and
// [projection.ViewUnspecified] to the policy's default view (see
// [projection.Policy.Select]).
func (t *ProjectionTranslator) TranslateView(spec projection.Spec, view projection.View) ([]string, error) {
	defer t.duration.Start()()

	sel, err := t.config.ResolveView(spec, view)
	if err != nil {
		return nil, err
	}

	return renderReturn(sel)
}

// renderReturn expresses sel as RETURN arguments.
func renderReturn(sel projection.Selection) ([]string, error) {
	switch {
	case sel.Mode == projection.ModeNone:
		return []string{Return, "0"}, nil
	case len(sel.Exclude) > 0:
		return nil, coreerrs.Wrap(projection.ErrUnsupportedSelection, "RETURN cannot exclude fields")
	case len(sel.Include) == 0:
		return nil, nil
	}

	out := make([]string, 0, len(sel.Include)+2) //nolint:mnd // the keyword and the count
	out = append(out, Return, strconv.Itoa(len(sel.Include)))

	for _, name := range sel.Include {
		if err := checkReturnName(name); err != nil {
			return nil, err
		}

		out = append(out, name)
	}

	return out, nil
}

// checkReturnName rejects a storage name RETURN cannot carry unambiguously: an
// empty one, one with white space or control characters, and the AS keyword,
// which RETURN parses as an alias marker.
func checkReturnName(name string) error {
	if name == "" || strings.EqualFold(name, "AS") ||
		strings.ContainsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return coreerrs.Wrapf(projection.ErrInvalidFieldPath, "%q is not a RETURN field", name)
	}

	return nil
}
