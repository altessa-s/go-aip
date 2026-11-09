// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"reflect"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/altessa-s/go-aip/backend/mongo/internal/bsonstruct"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// Bounds of the struct types [describeStruct] describes, and of the descriptions it keeps.
const (
	// maxStructFields bounds the fields of a struct type; a value of a type with more, or that inlines one with more, is opaque.
	maxStructFields = 1 << 12
	// maxCachedFields bounds the fields of all the descriptions kept: a program may construct struct types without end
	// (reflect.StructOf), and a type described once the bound is reached is described, and charged, again every time.
	maxCachedFields = 1 << 16
)

// structDescription is what the driver reads of a struct type before it encodes a value of it.
type structDescription struct {
	// fields are the fields the driver encodes, the exported ones not tagged "-".
	fields []encodedField
	// cost is the units describing the type costs: one, and per field one and one per [bytesPerUnit] bytes of its tag, besides the
	// cost of every struct type it inlines.
	cost int
	// opaque is set when the driver must not be asked to describe the type: it inlines itself, or inlines struct types
	// [maxStoredNesting] levels deep, or it or a type it inlines has more than [maxStructFields] fields.
	opaque bool
	// flat is the fields of the driver's description of the type, which lists the fields of the struct types it inlines in place.
	flat int
	// retained is the units the driver caches describing the type in a registry ([registryTypes]) besides the types of its own
	// fields: its description, one and flat, and for every struct type it inlines that type's retained units and the types of its
	// fields, [typeUnits] each.
	retained int
}

// encodedField is a struct field the driver encodes: its index, and the length of its key.
type encodedField struct {
	index    int
	keyBytes int
}

var (
	// structDescriptions holds the descriptions [describeStruct] keeps, by struct type.
	structDescriptions sync.Map // reflect.Type → *structDescription
	// cachedFields counts the fields of the descriptions kept, and the descriptions themselves.
	cachedFields atomic.Int64
	// inlinedTooDeep describes a struct type the inlining reaches [maxStoredNesting] levels deep; it is not kept, as the type
	// itself may inline fewer levels.
	inlinedTooDeep = &structDescription{opaque: true}
	// inlinesItself describes a struct type the inlining reaches again while describing it.
	inlinesItself = &structDescription{opaque: true}
)

// describeStruct returns the description of struct type t, the driver's, reached by inlining from the struct types on stack; nil
// once b is exhausted. Describing a type is charged to b whether its description is kept or not, before every field is read and
// its tag parsed, so that an evaluation costs the same with or without it: the driver describes the struct types a struct
// inlines (`bson:",inline"`) before it looks at a value, so a type inlining itself, even through a nil pointer, recurses forever
// and is opaque.
func describeStruct(t reflect.Type, stack []reflect.Type, b filtereval.Budget) *structDescription {
	if cached, ok := structDescriptions.Load(t); ok {
		d := cached.(*structDescription) //nolint:errcheck,forcetypeassert // the cache holds descriptions
		if !b.Charge(d.cost) {
			return nil
		}

		return d
	}

	switch {
	case slices.Contains(stack, t):
		return inlinesItself
	case len(stack) == maxStoredNesting:
		return inlinedTooDeep
	case !b.Charge(1):
		return nil
	}

	d := &structDescription{cost: 1}
	if t.NumField() > maxStructFields {
		d.opaque = true
		keepDescription(t, d)

		return d
	}

	stack = append(stack, t)
	for i := range t.NumField() {
		f := t.Field(i)

		units := 1 + len(f.Tag)/bytesPerUnit
		if !b.Charge(units) {
			return nil
		}

		d.cost += units

		field := bsonstruct.ReadField(f)
		if !field.Encoded {
			continue
		}

		d.fields = append(d.fields, encodedField{index: i, keyBytes: field.KeyBytes})

		ft := field.Inlined
		if ft == nil {
			d.flat++
			continue
		}

		inner := describeStruct(ft, stack, b)
		switch inner {
		case nil:
			return nil
		case inlinedTooDeep:
			return inlinedTooDeep
		}

		d.cost += inner.cost
		if inner.opaque {
			d.fields, d.opaque = nil, true
			break
		}

		d.flat += inner.flat
		d.retained += inner.retained + typeUnits*ft.NumField()
	}

	d.retained += 1 + d.flat
	keepDescription(t, d)

	return d
}

// keepDescription keeps the description d of t, within [maxCachedFields].
func keepDescription(t reflect.Type, d *structDescription) {
	n := int64(1 + len(d.fields))
	if cachedFields.Add(n) > maxCachedFields {
		cachedFields.Add(-n)
		return
	}

	if _, loaded := structDescriptions.LoadOrStore(t, d); loaded {
		cachedFields.Add(-n)
	}
}
