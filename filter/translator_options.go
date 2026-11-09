// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import (
	coremaps "github.com/altessa-s/go-atlas-core/collections/maps"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

//go:generate optgen generate --type=translatorOptions --output=translator_options_gen.go --option-type=TranslatorOption

// translatorOptions holds what a translator or evaluator needs on top of a [Policy] to address one backend: how the
// CEL-side (API) names of the policy are stored there.
type translatorOptions struct {
	// WithFieldMapping sets a mapping from CEL field names to storage names (database columns, document paths, data keys).
	// It lets a service expose user-friendly names while filtering against the underlying storage representation. The
	// policy still authorizes the CEL-side names; the mapping is copied into a [coremaps.ImmutableMap] so later
	// mutations of the caller's map cannot affect a constructed translator.
	//
	// Example:
	//
	//	trans, err := mongo.NewFilterTranslator(policy, mongo.WithFilterOptions(filter.WithFieldMapping(map[string]string{
	//	    "userName":  "user_name",
	//	    "createdAt": "created_at",
	//	})))
	fieldMapping *coremaps.ImmutableMap[string, string] `optfn:"FieldMapping=coremaps.NewImmutableMap[string, string]"`
}

// TranslatorContext is what [NewTranslatorContext] returns, as in orderby and projection: a [Policy] bound to the
// storage names of one backend. Unlike theirs it is the Policy type itself, so a translator takes the bound policy
// wherever a *Policy is accepted.
type TranslatorContext = Policy

// NewTranslatorContext binds policy to one backend: it returns a copy of policy that also applies the translator
// options (the storage mapping). policy itself is never modified, so one policy built per resource serves every
// translator and evaluator; the copy shares its compiled regex cache. It fails with [ErrPolicyRequired] for a nil
// policy: a translator never runs without the field rules of a policy, and a policy that accepts untrusted input
// always carries an allow-list or field access policy.
func NewTranslatorContext(policy *Policy, opts ...TranslatorOption) (*TranslatorContext, error) {
	if policy == nil {
		return nil, coreerrs.Wrap(ErrPolicyRequired, "nil filter policy")
	}

	o := newTranslatorOptions(opts...)
	bound := &Policy{options: policy.options, regexCache: policy.regexCache}
	bound.fieldMapping = o.fieldMapping
	bound.resolveFields()

	return bound, nil
}
