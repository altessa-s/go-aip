// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package projectiontest provides projection test helpers and a conformance suite every projection translator runs.
//
// MustParse and MustPolicy parse a fields expression and build a policy, failing the test on error. RunConformance checks one
// translator against the behavior shared by every backend: authorization, field mapping, required fields, every authorized
// field (AIP-157), views, ModeNone and exclusion — the last two by the backend's declared Capabilities, so a backend that
// cannot express them must reject them with the shared sentinel errors. A backend adapts its translator with Adapt and a
// decoder that reads its rendered output back into a backend-neutral Result.
//
// # Usage
//
//	func TestConformance(t *testing.T) {
//	    projectiontest.RunConformance(t, projectiontest.Backend{
//	        New: func(p *projection.Policy, storage ...projection.TranslatorOption) (projectiontest.Translator, error) {
//	            tr, err := meili.NewProjectionTranslator(p, meili.WithProjectionOptions(storage...))
//	            if err != nil {
//	                return nil, err
//	            }
//	            return projectiontest.Adapt(tr, decode), nil
//	        },
//	    })
//	}
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package projectiontest
