// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"fmt"

	"github.com/altessa-s/go-aip/filter"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// newEvaluator is evaluator.New over any field unless opts give an allow-list (filter.WithAnyField). It reads values without a
// value encoding unless opts give one (filtereval.WithValueEncoding(mongo.ValueEncoding()) pins the stored forms of driver types
// and structs).
//
// Each option is a [policyOptions] (withPolicy) or a filter.PolicyOption for the shared policy, a filter.TranslatorOption binding it
// to the data keys, or an evaluator option.
func newEvaluator(opts ...any) (*filtereval.Evaluator, error) {
	rules := []filter.PolicyOption{filter.WithAnyField(true)}

	var (
		binding  []filter.TranslatorOption
		evalOpts []filtereval.Option
	)

	for _, opt := range opts {
		switch opt := opt.(type) {
		case policyOptions:
			rules = append(rules, opt...)
		case filter.PolicyOption:
			rules = append(rules, opt)
		case filter.TranslatorOption:
			binding = append(binding, opt)
		case filtereval.Option:
			evalOpts = append(evalOpts, opt)
		default:
			return nil, fmt.Errorf("unexpected test evaluator option %T", opt)
		}
	}

	policy, err := filter.NewPolicy(rules...)
	if err != nil {
		return nil, err
	}

	return filtereval.New(policy, append(evalOpts, filtereval.WithFilterOptions(binding...))...)
}

// policyOptions are filter policy options of a test evaluator (see newEvaluator).
type policyOptions []filter.PolicyOption

// withPolicy groups policy options for newEvaluator.
func withPolicy(opts ...filter.PolicyOption) policyOptions { return opts }
