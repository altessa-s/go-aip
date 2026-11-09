// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/orderby"
)

// ascending and descending are the int32 values MongoDB expects in a sort
// document for the two directions.
const (
	ascending  int32 = 1
	descending int32 = -1
)

// OrderTranslator converts an [orderby.Spec] into a MongoDB [bson.D] sort
// document. It is safe for concurrent use after construction.
type OrderTranslator struct {
	config    *orderby.TranslatorContext
	telemetry *orderby.Telemetry
}

// NewOrderTranslator creates a new MongoDB sort translator bound to policy, the client-facing
// [orderby.Policy] shared by every backend of the resource; [WithOrderOptions] binds it to the
// storage names of this backend. Returns [orderby.ErrPolicyRequired] for a nil policy; Translate
// renders only orders the policy validates.
func NewOrderTranslator(policy *orderby.Policy, opts ...OrderOption) (*OrderTranslator, error) {
	o := newOrderOptions(opts...)

	ctx, err := orderby.NewTranslatorContext(policy, o.orderOptions...)
	if err != nil {
		return nil, err
	}

	return &OrderTranslator{config: ctx, telemetry: orderby.NewTelemetry(o.collector, "mongo")}, nil
}

// Translate converts an [orderby.Spec] into a MongoDB sort document.
// An empty Spec translates to a nil bson.D with no error — matching
// the Meilisearch translator's "nil for empty" shape so call sites can
// use the same `if sort != nil` check across backends.
// `SetSort(nil)` is valid in the MongoDB driver and is equivalent to
// omitting the sort entirely.
func (t *OrderTranslator) Translate(ob orderby.Spec) (bson.D, error) {
	done := t.telemetry.Start()
	defer done()

	var err error

	ob, err = t.config.Resolve(ob)
	if err != nil {
		return nil, err
	}

	// MongoDB orders null and missing values before every other value: an explicit placement equal to that is accepted.
	if err = orderby.CheckNativeOrdering(ob, orderby.NullsSmallest); err != nil {
		return nil, err
	}

	if ob.IsEmpty() {
		return nil, nil
	}

	out := make(bson.D, 0, len(ob.Keys))
	for _, k := range ob.Keys {
		value := ascending
		if k.IsDescending() {
			value = descending
		}

		out = append(out, bson.E{
			Key:   k.Name,
			Value: value,
		})
	}

	return out, nil
}
