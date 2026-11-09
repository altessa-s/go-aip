// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis

import (
	"github.com/altessa-s/go-aip/orderby"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// SortBy is the single-field sort instruction RediSearch's
// FT.SEARCH ... SORTBY clause expects. A zero-value SortBy (Field == "")
// signals that no SORTBY clause should be appended.
type SortBy struct {
	Field      string
	Descending bool
}

// OrderTranslator converts an [orderby.Spec] into a single-field RediSearch
// sort instruction. It is safe for concurrent use after construction.
type OrderTranslator struct {
	config    *orderby.TranslatorContext
	telemetry *orderby.Telemetry
}

// NewOrderTranslator creates a new RediSearch sort translator bound to policy, the client-facing
// [orderby.Policy] shared by every backend of the resource; [WithOrderOptions] binds it to the
// storage names of this backend. Returns [orderby.ErrPolicyRequired] for a nil policy; Translate
// renders only orders the policy validates.
func NewOrderTranslator(policy *orderby.Policy, opts ...OrderOption) (*OrderTranslator, error) {
	o := newOrderOptions(opts...)

	ctx, err := orderby.NewTranslatorContext(policy, o.orderOptions...)
	if err != nil {
		return nil, err
	}

	return &OrderTranslator{config: ctx, telemetry: orderby.NewTelemetry(o.collector, "redis")}, nil
}

// Translate converts an [orderby.Spec] into a [SortBy]. Returns a
// zero-value SortBy for an empty input and [orderby.ErrTooManySortKeys]
// when more than one resolved key is present (a client key plus a
// tie-breaker counts as two) — FT.SEARCH ... SORTBY accepts a single field
// only. ErrTooManySortKeys wraps [orderby.ErrUnsupportedOrdering], the
// sentinel every translator returns for an order its backend cannot
// represent. Any explicit null placement or collation also fails with
// [orderby.ErrUnsupportedOrdering], and a storage name that is empty, not
// valid UTF-8, holds white space or control characters or an empty
// dot-separated segment with [orderby.ErrInvalidFieldPath].
func (t *OrderTranslator) Translate(ob orderby.Spec) (SortBy, error) {
	done := t.telemetry.Start()
	defer done()

	var err error

	ob, err = t.config.Resolve(ob)
	if err != nil {
		return SortBy{}, err
	}

	if err = orderby.CheckBasicOrdering(ob); err != nil {
		return SortBy{}, err
	}

	if ob.IsEmpty() {
		return SortBy{}, nil
	}

	if len(ob.Keys) > 1 {
		return SortBy{}, coreerrs.Wrapf(orderby.ErrTooManySortKeys,
			"redis supports a single SORTBY field, got %d", len(ob.Keys))
	}

	k := ob.Keys[0]
	if !validAttribute(k.Name) {
		return SortBy{}, coreerrs.Wrapf(orderby.ErrInvalidFieldPath, "%q is not a valid RediSearch sort attribute", k.Name)
	}

	return SortBy{
		Field:      k.Name,
		Descending: k.IsDescending(),
	}, nil
}
