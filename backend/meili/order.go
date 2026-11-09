// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili

import (
	"github.com/altessa-s/go-aip/orderby"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// OrderTranslator converts an [orderby.Spec] into the Meilisearch `sort`
// argument: a `[]string` of "field:asc" / "field:desc" entries. It is
// safe for concurrent use after construction.
type OrderTranslator struct {
	config    *orderby.TranslatorContext
	telemetry *orderby.Telemetry
}

// NewOrderTranslator creates a new Meilisearch sort translator bound to policy, the client-facing
// [orderby.Policy] shared by every backend of the resource; [WithOrderOptions] binds it to the
// storage names of this backend. Returns [orderby.ErrPolicyRequired] for a nil policy; Translate
// renders only orders the policy validates.
func NewOrderTranslator(policy *orderby.Policy, opts ...OrderOption) (*OrderTranslator, error) {
	o := newOrderOptions(opts...)

	ctx, err := orderby.NewTranslatorContext(policy, o.orderOptions...)
	if err != nil {
		return nil, err
	}

	return &OrderTranslator{config: ctx, telemetry: orderby.NewTelemetry(o.collector, "meili")}, nil
}

// Translate converts an [orderby.Spec] into the Meilisearch sort
// argument. Returns a nil slice for an empty Spec so callers can omit
// the `sort` parameter entirely. A storage name that is not one or more
// dot-separated segments of ASCII letters, digits, `_` and `-` fails with
// [orderby.ErrInvalidFieldPath]: Meilisearch splits a sort entry at its last
// `:` and the name is written as it is.
func (t *OrderTranslator) Translate(ob orderby.Spec) ([]string, error) {
	done := t.telemetry.Start()
	defer done()

	var err error

	ob, err = t.config.Resolve(ob)
	if err != nil {
		return nil, err
	}

	// Meilisearch places documents whose sort attribute is null or missing after every other document in both directions: an
	// explicit nulls last is accepted, nulls first is rejected.
	if err = orderby.CheckNativeOrdering(ob, orderby.NullsAlwaysLast); err != nil {
		return nil, err
	}

	if ob.IsEmpty() {
		return nil, nil
	}

	out := make([]string, 0, len(ob.Keys))
	for _, k := range ob.Keys {
		direction := "asc"
		if k.IsDescending() {
			direction = "desc"
		}

		if !validAttribute(k.Name) {
			return nil, coreerrs.Wrapf(orderby.ErrInvalidFieldPath, "%q is not a valid Meilisearch sort attribute", k.Name)
		}

		out = append(out, k.Name+":"+direction)
	}

	return out, nil
}
