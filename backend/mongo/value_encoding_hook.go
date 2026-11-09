// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"bytes"
	"errors"
	"reflect"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-atlas-core/types/optional"

	"github.com/altessa-s/go-aip/backend/mongo/internal/bsonraw"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// marshal encodes v as [storedRegistry] does, as the value of a document's only element, letting the values that encode themselves
// write at most hookBytes in all. A panic of the driver fails the encoding ([guarded]) and keeps h out of the pool, whose caches it
// may have left part-way.
func (h *hookEncoder) marshal(v any, hookBytes int) (raw bson.RawValue, err error) {
	h.left = hookBytes

	var buf bytes.Buffer

	enc := bson.NewEncoder(bson.NewDocumentWriter(&buf))
	enc.SetRegistry(h.registry)

	err = guarded(func() error {
		if encodeErr := enc.Encode(bson.D{{Key: "v", Value: v}}); encodeErr != nil {
			return encodeErr
		}

		var lookupErr error

		raw, lookupErr = bson.Raw(buf.Bytes()).LookupErr("v")

		return lookupErr
	})
	if errors.Is(err, errDriverPanic) {
		h.types.full = true
	}

	return raw, err
}

// errHookOutputGrew fails an encoding in which the values encoding themselves return more than [encodable] measured.
var errHookOutputGrew = errors.New("a value encodes itself to more than was measured")

// errNoHook fails the encoding of a value that does not encode itself through the interface its encoder was looked up for.
var errNoHook = errors.New("the value does not encode itself")

// hookEncoder is a registry that encodes as [storedRegistry] does, except that the values encoding themselves through
// bson.ValueMarshaler or bson.Marshaler — whose hooks the driver calls again after [encodable] called them to measure their output —
// may write at most left bytes in all: a hook returning more the second time, as the type's own code may, fails the encoding
// before the driver copies its output. A registry holds the bound of one encoding at a time; [hookEncoders] pools them, and types
// accounts for what the driver caches in it.
type hookEncoder struct {
	registry *bson.Registry
	left     int
	types    registryTypes
}

// hookEncoders keeps up to [maxPooledRegistries] idle [hookEncoder] registries, which are costly to build. The driver caches in a
// registry what it learns of every type it meets, without bound, so a registry returns to the pool only while those caches stay
// within [maxRegistryUnits] ([registryTypes]) and the pool has room: one whose encoding would take them beyond, or that finds the
// pool full, is dropped after it, and a new one is built when the pool is empty. The idle registries retain at most
// [maxPooledRegistries] × ([maxRegistryUnits] + the entries a registry is built with); those in use are as many as the encodings
// running, each dropped or pooled when its encoding ends.
var hookEncoders = make(chan *hookEncoder, maxPooledRegistries)

// acquireHookEncoder takes an idle registry from [hookEncoders], or returns nil when there is none.
func acquireHookEncoder() *hookEncoder {
	select {
	case h := <-hookEncoders:
		return h
	default:
		return nil
	}
}

// release returns h, if any, to [hookEncoders], unless the types it met fill it or the pool is full.
func (h *hookEncoder) release() {
	if h == nil || h.types.full {
		return
	}

	select {
	case hookEncoders <- h:
	default:
	}
}

// Bounds of what the driver caches in a pooled registry.
const (
	// maxRegistryUnits bounds the units of the driver's caches in a registry [hookEncoders] keeps, besides those it is built with:
	// a program may construct types without end (reflect.StructOf, SliceOf, MapOf, ArrayOf, ...), each of which a registry that
	// meets it would keep.
	maxRegistryUnits = 1 << 14
	// maxPooledRegistries bounds the idle registries [hookEncoders] keeps.
	maxPooledRegistries = 16
	// typeUnits is the most entries the driver caches for a type it meets: its encoder, its decoder (a struct field's type), and
	// the encoder of what it points to (a pointer type). A struct type's description is counted besides ([structDescription]).
	typeUnits = 3
)

// registryTypes accounts for what the driver may cache in a registry, a superset of the types it meets encoding the values
// [encodable] walks: every type of a value walked and the type of its elements or of what it points to, which the driver looks up
// even for an empty or nil value; for a struct type, the types of all its fields and its description, of the struct types it
// inlines included. A type counts [typeUnits], a struct's description its retained units, each once per registry; the registry is
// full once they would exceed [maxRegistryUnits], and then stops counting.
type registryTypes struct {
	// seen holds the types counted, true for a struct type whose description is.
	seen  map[reflect.Type]bool
	units int
	full  bool
}

// add counts units for t, or fills the registry.
func (r *registryTypes) add(t reflect.Type, units int, described bool) {
	if r.units+units > maxRegistryUnits {
		r.full = true
		return
	}

	r.units += units
	if r.seen == nil {
		r.seen = make(map[reflect.Type]bool)
	}

	r.seen[t] = described
}

// note counts t, which the registry meets, and the type of its elements or of what it points to.
func (r *registryTypes) note(t reflect.Type) {
	if _, seen := r.seen[t]; seen || r.full {
		return
	}

	r.add(t, typeUnits, false)

	switch t.Kind() {
	case reflect.Array, reflect.Map, reflect.Pointer, reflect.Slice:
		if _, seen := r.seen[t.Elem()]; !seen {
			r.add(t.Elem(), typeUnits, false)
		}
	default:
	}
}

// describe counts struct type t, described as d, which the driver describes: the description, and the types of its fields, which
// it looks up while describing it. The fields of a type d is described with are within [maxStructFields], and charged.
func (r *registryTypes) describe(t reflect.Type, d *structDescription) {
	if r.full || r.seen[t] {
		return
	}

	r.add(t, d.retained, true)

	for f := range t.Fields() {
		r.note(f.Type)
	}
}

// optionalEncoder is the encoder [storedRegistry] gives an Optional, and a pointer to one: its value, or null, without its hook.
var optionalEncoder = func() bson.ValueEncoder {
	enc, err := storedRegistry.LookupEncoder(reflect.TypeFor[optional.Optional[int]]())
	if err != nil {
		panic(err)
	}

	return enc
}()

// newHookEncoder returns a [hookEncoder] about to meet types, counted before it is built.
func newHookEncoder(types registryTypes) *hookEncoder {
	h := &hookEncoder{registry: NewRegistry(), types: types}
	for _, t := range [...]reflect.Type{documentType, rawValueType} { // the types of every encoding: the document, a hook's output
		h.types.note(t)
	}

	for _, iface := range hookInterfaces {
		h.registry.RegisterInterfaceEncoder(iface, bson.ValueEncoderFunc(
			func(ec bson.EncodeContext, vw bson.ValueWriter, val reflect.Value) error {
				return h.encode(ec, vw, val, iface)
			}))
	}

	return h
}

// encode writes val, which encodes itself through iface, as the driver does (an addressable value through its pointer, a nil
// pointer whose pointee encodes itself as null) once its output is found within the bytes left.
func (h *hookEncoder) encode(ec bson.EncodeContext, vw bson.ValueWriter, val reflect.Value, iface reflect.Type) error {
	if !val.IsValid() {
		return errNoHook
	}

	if t := val.Type(); iface == valueMarshalerType && (optional.IsOptionalType(t) ||
		(t.Kind() == reflect.Pointer && optional.IsOptionalType(t.Elem()))) {
		return optionalEncoder.EncodeValue(ec, vw, val)
	}

	switch t := val.Type(); {
	case t.Implements(iface):
		if val.Kind() == reflect.Pointer && val.IsNil() && pointee(t).Implements(iface) {
			return vw.WriteNull()
		}
	case reflect.PointerTo(t).Implements(iface) && val.CanAddr():
		val = val.Addr()
	default:
		return errNoHook
	}

	typ, data, err := callHook(val, iface)
	if err != nil {
		return err
	}

	if len(data) > h.left {
		return errHookOutputGrew
	}

	if bsonraw.Walk(typ, data, maxStoredNesting, nil) != nil { // the second output, before the driver copies it
		return errMalformedHookOutput
	}

	h.left -= len(data)

	raw, err := ec.LookupEncoder(rawValueType)
	if err != nil {
		return err
	}

	return raw.EncodeValue(ec, vw, reflect.ValueOf(bson.RawValue{Type: typ, Value: data}))
}

// callHook returns the encoding the hook of recv through iface returns: a BSON value, or a document for bson.Marshaler. What the
// hook does to produce it is the type's own code.
func callHook(recv reflect.Value, iface reflect.Type) (bson.Type, []byte, error) {
	if !recv.CanInterface() {
		return 0, nil, errNoHook
	}

	if iface == valueMarshalerType {
		h, ok := reflect.TypeAssert[bson.ValueMarshaler](recv)
		if !ok {
			return 0, nil, errNoHook
		}

		typ, data, err := h.MarshalBSONValue()

		return bson.Type(typ), data, err
	}

	h, ok := reflect.TypeAssert[bson.Marshaler](recv)
	if !ok {
		return 0, nil, errNoHook
	}

	data, err := h.MarshalBSON()

	return bson.TypeEmbeddedDocument, data, err
}

// encodable reports whether the driver can encode v in bounded time, stack and memory: the driver follows pointers, interfaces,
// struct fields, map values and elements without a cycle check or depth limit, so a cyclic value overflows the stack, a fatal
// error that no recover catches, and it describes the struct types a struct inlines (`bson:",inline"`) before it looks at a
// value, so a type that inlines itself, even through a nil pointer, overflows it too. encodable walks v as the driver does —
// exported struct fields not tagged "-", the value of an Optional, the output of any other custom encoding, which it calls to
// measure — within [maxPointerHops] consecutive pointers, [maxStoredNesting] levels, [maxStoredNodes] values and an estimate of
// [maxDocumentBytes] encoded bytes (keys included), rejecting what the driver would spend work on in proportion to a value
// before writing it: a struct type that inlines itself or has more than [maxStructFields] fields, a decimal.Decimal beyond
// [maxCoefficientBits], a map keyed by a type that encodes itself as text. A cycle exceeds the first two bounds. The walk, the
// description of the struct types included ([describeStruct]), is charged to b; hookBytes is what the custom encodings returned.
// The types the driver meets encoding v are counted in types.
func encodable(v any, b filtereval.Budget, types *registryTypes) (hookBytes int, ok bool) {
	w := encodeWalk{budget: b, nodes: maxStoredNodes, bytes: maxDocumentBytes, types: types}
	ok = w.value(reflect.ValueOf(v), 0, 0)

	return w.hookBytes, ok
}
