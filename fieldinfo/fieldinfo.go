// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldinfo

import (
	"slices"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/lru"
)

// annotationCacheSize bounds the field descriptors whose decoded
// google.api.field_info annotation is cached. The cache is shared by the
// package; an entry not used recently is evicted past the bound, so
// dynamic descriptors are never retained without limit.
const annotationCacheSize = 4096

// Info is the google.api.field_info annotation of a field.
type Info struct {
	// Format is the standard format of the field's value, [Unspecified]
	// when the field declares none.
	Format Format
	// ReferencedTypes lists the message types a generic field (a
	// google.protobuf.Any) may hold: simple names of the field's package,
	// fully qualified names, or "*" for any type. It is exposed only;
	// nothing validates a value against it.
	ReferencedTypes []string
}

// annotation is the cached, immutable decoding of one field's annotation.
type annotation struct {
	format          Format
	referencedTypes []string
}

var annotationCache = mustCache[protoreflect.FieldDescriptor, annotation](annotationCacheSize)

func mustCache[K comparable, V any](size int) *lru.Cache[K, V] {
	c, err := lru.NewCache[K, V](size)
	if err != nil {
		panic(err)
	}

	return c
}

// Lookup returns the google.api.field_info annotation of fd, the zero Info
// when fd carries none. ReferencedTypes is an owned copy. The decoded
// annotation is cached per descriptor (see [annotationCacheSize]).
func Lookup(fd protoreflect.FieldDescriptor) Info {
	a := cachedAnnotation(fd)

	return Info{Format: a.format, ReferencedTypes: slices.Clone(a.referencedTypes)}
}

// FormatOf returns the format fd declares, [Unspecified] when none. Unlike
// [Lookup] it allocates nothing.
func FormatOf(fd protoreflect.FieldDescriptor) Format {
	return cachedAnnotation(fd).format
}

func cachedAnnotation(fd protoreflect.FieldDescriptor) annotation {
	if fd == nil {
		return annotation{}
	}

	if a, ok := annotationCache.Get(fd); ok {
		return a
	}

	a := decode(fd)
	annotationCache.Put(fd, a)

	return a
}

// decode reads the field_info extension from the descriptor options.
func decode(fd protoreflect.FieldDescriptor) annotation {
	opts := fd.Options()
	if opts == nil || !proto.HasExtension(opts, annotations.E_FieldInfo) {
		return annotation{}
	}

	info, ok := proto.GetExtension(opts, annotations.E_FieldInfo).(*annotations.FieldInfo)
	if !ok || info == nil {
		return annotation{}
	}

	a := annotation{format: info.GetFormat()}
	for _, ref := range info.GetReferencedTypes() {
		a.referencedTypes = append(a.referencedTypes, ref.GetTypeName())
	}

	return a
}

// isStringValue reports whether the values fd holds are strings: a singular
// or repeated string field, or a map whose values are strings. AIP-202
// formats apply to string fields only; a format on any other field is
// ignored by validation.
func isStringValue(fd protoreflect.FieldDescriptor) bool {
	if fd.IsMap() {
		return fd.MapValue().Kind() == protoreflect.StringKind
	}

	return fd.Kind() == protoreflect.StringKind
}
