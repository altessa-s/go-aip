// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/metrics"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

//go:generate optgen generate

// DefaultMaxDataBudget bounds the work spent reading data in one evaluation.
const DefaultMaxDataBudget = 1 << 22

type options struct {
	// WithCollector records evaluation and preparation durations into a [metrics.Meter]; nil disables it.
	collector metrics.Meter `opt:"Collector" optgen:"notnil"`
	// WithComparator takes precedence over built-in scalar comparisons when handled is true.
	comparator Comparator

	// WithFilterOptions binds the shared policy to the keys of the evaluated data ([filter.WithFieldMapping]).
	filterOptions []filter.TranslatorOption `opt:"FilterOptions" optgen:"append" optval:"clone=eager"`
	maxDataBudget int                       `optgen:"default=DefaultMaxDataBudget" optval:"positive"`
	valueEncoding ValueEncoding
	// WithProfile selects an explicit semantic contract. Nil selects native evaluation.
	profile Profile `opt:"Profile"`
	// valueDecoder adapts nullable and driver-owned SQL values.
	valueDecoder ValueDecoder
}

// executionConfig combines the immutable policy with evaluator-only settings.
type executionConfig struct {
	*filter.Policy
	options
	// compiler compiles every filter of a profile ([Compiler], [Binder]); nil without a profile.
	compiler Compiler
	// accessor reads fields for Lookup when the profile provides it ([FieldAccessor]).
	accessor  FieldAccessor
	telemetry *telemetry
}

func newExecutionConfig(shared *filter.Policy, opts ...Option) (*executionConfig, error) {
	o := newOptions(opts...)
	if nilcheck.IsNil(o.profile) {
		o.profile = nil
	}

	policy, err := filter.NewTranslatorContext(shared, o.filterOptions...)
	if err != nil {
		return nil, err
	}

	config := &executionConfig{Policy: policy, options: *o, telemetry: newTelemetry(o.collector)}
	if o.profile == nil {
		if o.valueDecoder != nil {
			return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "decoder requires a compiled profile")
		}

		return config, nil
	}

	if err = o.profile.ValidatePolicy(policy); err != nil {
		return nil, err
	}

	switch profile := o.profile.(type) {
	case Binder:
		if config.compiler, err = profile.BindEvaluator(policy, o.valueEncoding); err != nil {
			return nil, err
		}

		if nilcheck.IsNil(config.compiler) {
			return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "profile bound no compiler")
		}
	case Compiler:
		if o.valueEncoding != nil {
			return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "compiled profile uses ValueDecoder rather than ValueEncoding")
		}

		config.compiler = profile
	default:
		return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "profile must implement Compiler or Binder")
	}

	if accessor, ok := config.compiler.(FieldAccessor); ok {
		config.accessor = accessor
	} else if accessor, ok := o.profile.(FieldAccessor); ok {
		config.accessor = accessor
	}

	return config, nil
}

func (c *executionConfig) MaxDataBudget() int { return c.maxDataBudget }
