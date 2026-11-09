// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projectiontest_test

import (
	"testing"

	"github.com/altessa-s/go-aip/projection"
	"github.com/altessa-s/go-aip/projection/projectiontest"
)

// reference renders the resolved selection itself: the behavior every
// backend is measured against.
type reference struct{ ctx *projection.TranslatorContext }

func (r reference) Translate(spec projection.Spec) (projection.Selection, error) {
	return r.ctx.Resolve(spec)
}

func (r reference) TranslateView(spec projection.Spec, view projection.View) (projection.Selection, error) {
	return r.ctx.ResolveView(spec, view)
}

func decode(sel projection.Selection) (projectiontest.Result, error) {
	switch {
	case sel.Mode == projection.ModeNone:
		return projectiontest.Result{None: true}, nil
	case sel.IsAll():
		return projectiontest.Result{All: true}, nil
	default:
		return projectiontest.Result{Include: sel.Include, Exclude: sel.Exclude}, nil
	}
}

func TestRunConformance(t *testing.T) {
	t.Parallel()

	projectiontest.RunConformance(t, projectiontest.Backend{
		New: func(p *projection.Policy, storage ...projection.TranslatorOption) (projectiontest.Translator, error) {
			ctx, err := projection.NewTranslatorContext(p, storage...)
			if err != nil {
				return nil, err
			}

			return projectiontest.Adapt(reference{ctx: ctx}, decode), nil
		},
		Capabilities: projectiontest.Capabilities{Exclusion: true, None: true},
	})
}

func TestMustParse(t *testing.T) {
	t.Parallel()

	spec := projectiontest.MustParse(t, "name, address.city")
	if got := spec.Paths; len(got) != 2 || got[0] != "address.city" || got[1] != "name" {
		t.Fatalf("Paths = %v", got)
	}

	if spec := projectiontest.MustParse(t, "*"); spec.Mode != projection.ModeAll {
		t.Fatalf("Mode = %v, want ModeAll", spec.Mode)
	}
}

func TestMustPolicy(t *testing.T) {
	t.Parallel()

	p := projectiontest.MustPolicy(t, projection.WithAllowedFields("name"))
	if _, err := p.Validate(projection.Spec{Paths: []string{"name"}}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
