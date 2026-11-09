// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory

import (
	"github.com/altessa-s/go-aip/projection"
)

// ProjectionTranslator converts a [projection.Spec] into a [Projection] applied to
// documents held in memory. It is safe for concurrent use after
// construction.
type ProjectionTranslator struct {
	config   *projection.TranslatorContext
	duration *projection.Telemetry
}

// NewProjectionTranslator creates an in-memory projection translator bound to policy,
// the client-facing [projection.Policy] shared by every backend of the
// resource. Storage options go through [WithProjectionOptions]. It returns
// the construction errors of [projection.NewTranslatorContext]; every
// selection — inclusion, exclusion and none — can be applied in memory.
func NewProjectionTranslator(policy *projection.Policy, opts ...ProjectionOption) (*ProjectionTranslator, error) {
	o := newProjectionOptions(opts...)

	ctx, err := projection.NewTranslatorContext(policy, o.projectionOptions...)
	if err != nil {
		return nil, err
	}

	return &ProjectionTranslator{config: ctx, duration: projection.NewTelemetry(o.collector, "memory")}, nil
}

// Translate resolves a read mask against the policy and returns the
// [Projection] to apply to each document. An omitted mask and "*" select
// every authorized field (AIP-157).
func (t *ProjectionTranslator) Translate(spec projection.Spec) (*Projection, error) {
	defer t.duration.Start()()

	sel, err := t.config.Resolve(spec)
	if err != nil {
		return nil, err
	}

	return newProjection(sel), nil
}

// TranslateView is [ProjectionTranslator.Translate] for a method with a resource view:
// a present read mask decides the fields, an omitted one defers to view, and
// [projection.ViewUnspecified] to the policy's default view (see
// [projection.Policy.Select]).
func (t *ProjectionTranslator) TranslateView(spec projection.Spec, view projection.View) (*Projection, error) {
	defer t.duration.Start()()

	sel, err := t.config.ResolveView(spec, view)
	if err != nil {
		return nil, err
	}

	return newProjection(sel), nil
}
