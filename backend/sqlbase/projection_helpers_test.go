// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"fmt"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/projection"
)

// split builds a policy from the policy options in opts and returns it with
// the translator options, so tests can list both kinds side by side.
func split(opts ...any) (*projection.Policy, []projection.TranslatorOption, error) {
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

	p, err := projection.NewPolicy(policy...)

	return p, storage, err
}

func newContext(style sqlbase.ColumnResolver, opts ...any) (*projection.TranslatorContext, error) {
	p, storage, err := split(opts...)
	if err != nil {
		return nil, err
	}

	return sqlbase.NewProjectionContext(p, style, storage...)
}

func newTranslator(d sqlbase.ProjectionDialect, opts ...any) (*sqlbase.ProjectionTranslator, error) {
	p, storage, err := split(opts...)
	if err != nil {
		return nil, err
	}

	return sqlbase.NewProjectionTranslator(p, d, sqlbase.WithProjectionOptions(storage...))
}
