// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection_test

import (
	"fmt"

	"github.com/altessa-s/go-aip/projection"
)

// split sorts opts into the options of each layer, so table tests can list
// both kinds side by side.
func split(opts ...any) ([]projection.PolicyOption, []projection.TranslatorOption) {
	var (
		policy  []projection.PolicyOption
		storage []projection.TranslatorOption
	)

	for _, opt := range opts {
		switch opt := opt.(type) {
		case projection.PolicyOption:
			policy = append(policy, opt)
		case projection.TranslatorOption:
			storage = append(storage, opt)
		default:
			panic(fmt.Sprintf("unexpected option %T", opt))
		}
	}

	return policy, storage
}

// newContext builds a policy from the policy options in opts and binds it
// with the translator options.
func newContext(opts ...any) (*projection.TranslatorContext, error) {
	policyOpts, storage := split(opts...)

	policy, err := projection.NewPolicy(policyOpts...)
	if err != nil {
		return nil, err
	}

	return projection.NewTranslatorContext(policy, storage...)
}
