// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory

import (
	"cmp"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/pagination/pagetoken"

	metrics "github.com/altessa-s/go-aip/metrics"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Comparator orders two records: negative when a sorts before b, positive when after, zero when the order does not distinguish
// them. It is a strict weak order suitable for [slices.SortFunc] and [slices.SortStableFunc], and safe for concurrent use. It
// never modifies the records.
type Comparator func(a, b map[string]any) int

// OrderTranslator converts an [orderby.Spec] into a [Comparator]. It is safe for concurrent use after construction.
type OrderTranslator struct {
	config    *orderby.TranslatorContext
	telemetry *metrics.TranslateTimer
	encoding  evaluator.ValueEncoding
	maxWork   int
}

// NewOrderTranslator creates an in-memory sort translator bound to policy, the client-facing [orderby.Policy] shared by every backend
// of the resource; [WithOrderOptions] binds it to the storage names of the records. Returns [orderby.ErrPolicyRequired] for a nil
// policy; Translate builds comparators only for orders the policy validates.
func NewOrderTranslator(policy *orderby.Policy, opts ...OrderOption) (*OrderTranslator, error) {
	o := newOrderOptions(opts...)

	ctx, err := orderby.NewTranslatorContext(policy, o.orderOptions...)
	if err != nil {
		return nil, err
	}

	return &OrderTranslator{
		config:    ctx,
		telemetry: orderby.NewTelemetry(o.collector, "memory"),
		encoding:  o.valueEncoding,
		maxWork:   o.maxDataBudget,
	}, nil
}

// Translate converts an [orderby.Spec] into a [Comparator] over records keyed by storage names. Each key is read as a dotted path
// through nested documents; the comparator compares key by key, in each key's direction and null placement. An empty resolved
// order translates to a nil Comparator with no error, matching the nil output of the MongoDB and Meilisearch translators. A
// collation fails with [orderby.ErrUnsupportedOrdering]; every explicit null placement is supported.
func (t *OrderTranslator) Translate(ob orderby.Spec) (Comparator, error) {
	done := t.telemetry.Start()
	defer done()

	var err error

	ob, err = t.config.Resolve(ob)
	if err != nil {
		return nil, err
	}

	if ob.IsEmpty() {
		return nil, nil //nolint:nilnil // a nil Comparator is the documented "no ordering" output, like the nil MongoDB sort.
	}

	keys := make([]sortKey, len(ob.Keys))
	for i, k := range ob.Keys {
		if k.Collation != "" {
			return nil, coreerrs.Wrapf(orderby.ErrUnsupportedOrdering, "%s: collation", k.Name)
		}

		nulls := k.Nulls
		if nulls == orderby.NullsDefault {
			nulls = orderby.NullsSmallest(k)
		}

		keys[i] = sortKey{path: splitPath(k.Name), desc: k.IsDescending(), nullsFirst: nulls == orderby.NullsFirst}
	}

	r := reader{encoding: t.encoding, maxWork: t.maxWork}

	return func(a, b map[string]any) int {
		for _, k := range keys {
			if c := k.compare(r.read(a, k.path), r.read(b, k.path)); c != 0 {
				return c
			}
		}

		return 0
	}, nil
}

// sortKey is one resolved key of a comparator.
type sortKey struct {
	path       []string
	desc       bool
	nullsFirst bool
}

// compare orders two read values under k: null by the key's placement whatever the direction, any other value by its class and
// value, reversed for a descending key.
func (k sortKey) compare(a, b value) int {
	switch an, bn := a.class == classNull, b.class == classNull; {
	case an && bn:
		return 0
	case an:
		return k.nullSign()
	case bn:
		return -k.nullSign()
	}

	c := a.compare(b)
	if k.desc {
		return -c
	}

	return c
}

// nullSign is the order of a null before (-1) or after (+1) a value.
func (k sortKey) nullSign() int {
	if k.nullsFirst {
		return -1
	}

	return 1
}

// A sort value is read in its stored form with the filter evaluator's reading ([evaluator.StoredValue]): pointers and interfaces
// are followed, a value whose form the evaluator.ValueEncoding defines is read as the encoding stores it, documents are maps keyed
// by strings, and a scalar is read by its kind. The scalar is then ordered by the memory listing engine's portable scalar order
// ([pagetoken.SortValue]).

// class is the rank of a read value: null, then every scalar, then every unsortable value.
type class uint8

const (
	classNull class = iota
	classScalar
	classUnsortable
)

// value is one read sort value.
type value struct {
	scalar pagetoken.SortValue
	class  class
}

// compare orders two non-null values: scalars by the portable scalar order, every scalar before every unsortable value, and
// unsortable values equal to each other.
func (v value) compare(w value) int {
	if v.class != w.class {
		return cmp.Compare(v.class, w.class)
	}

	if v.class == classScalar {
		return v.scalar.Compare(w.scalar)
	}

	return 0
}

// splitPath splits a dotted storage path into its segments.
func splitPath(name string) []string { return strings.Split(name, ".") }

// reader reads sort values from records.
type reader struct {
	encoding evaluator.ValueEncoding
	maxWork  int
}

// budget is the work one read may spend reading stored forms.
type budget struct{ left int }

// Charge implements [evaluator.Budget].
func (b *budget) Charge(units int) bool {
	if units < 0 || units > b.left {
		b.left = -1
		return false
	}

	b.left -= units

	return true
}

// read returns the value at path in record: null when a segment is missing, null, or not a document.
func (r reader) read(record map[string]any, path []string) value {
	b := &budget{left: r.maxWork}

	var cur any = record
	for _, seg := range path {
		doc, ok := evaluator.StoredValue(cur, r.encoding, b).(map[string]any)
		if !ok {
			return value{}
		}

		if cur, ok = doc[seg]; !ok {
			return value{}
		}
	}

	return r.scalar(evaluator.StoredValue(cur, r.encoding, b), b)
}

// scalar classifies a stored value: null, a scalar of the portable order, or unsortable.
func (r reader) scalar(v any, b *budget) value {
	if v == nil {
		return value{}
	}

	if b.left < 0 {
		return value{class: classUnsortable}
	}

	switch x := v.(type) {
	case []any, map[string]any, []byte:
		return value{class: classUnsortable}
	case uint64: // the stored form keeps only an unsigned value above the int64 range
		v = decimal.NewFromUint64(x)
	}

	s, err := pagetoken.ScalarPositionCodec{}.Normalize(v, time.Nanosecond)
	if err != nil {
		return value{class: classUnsortable}
	}

	return value{scalar: s, class: classScalar}
}
