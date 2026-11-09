// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package behavior

import (
	"slices"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/lru"
)

// AnnotationCacheSize bounds the field descriptors whose decoded
// google.api.field_behavior annotation is cached. The cache is shared by the
// process; past the bound the least recently used entry is evicted, so dynamic
// descriptors are never retained without limit.
const AnnotationCacheSize = 4096

var annotationCache = mustCache[protoreflect.FieldDescriptor, []annotations.FieldBehavior](AnnotationCacheSize)

func mustCache[K comparable, V any](size int) *lru.Cache[K, V] {
	c, err := lru.NewCache[K, V](size)
	if err != nil {
		panic(err)
	}

	return c
}

// Get returns an owned copy of the field annotations.
func Get(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
	return slices.Clone(Shared(fd))
}

// Shared returns the cached annotations of fd without copying them. The slice
// is shared by every caller and must not be modified; use [Get] for an owned
// copy. The decoded annotation is cached per descriptor (see
// [AnnotationCacheSize]).
func Shared(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
	if fd == nil {
		return nil
	}

	if got, ok := annotationCache.Get(fd); ok {
		return got
	}

	got := resolve(fd)
	annotationCache.Put(fd, got)

	return got
}

// resolve decodes the field_behavior extension from the descriptor options.
// The result is freshly allocated by the extension conversion and never
// shared with the descriptor.
func resolve(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
	opts := fd.Options()
	if opts == nil {
		return nil
	}

	behaviors, ok := proto.GetExtension(opts, annotations.E_FieldBehavior).([]annotations.FieldBehavior)
	if !ok || len(behaviors) == 0 {
		return nil
	}

	// Trim the capacity so an append by a careless caller of Shared cannot
	// write into the cached backing array.
	return slices.Clip(behaviors)
}

// Has reports whether fd carries the given behavior.
func Has(fd protoreflect.FieldDescriptor, b annotations.FieldBehavior) bool {
	return slices.Contains(Shared(fd), b)
}

// HasAny reports whether fd carries any of the given behaviors. With an empty
// set it returns false.
func HasAny(fd protoreflect.FieldDescriptor, set ...annotations.FieldBehavior) bool {
	if len(set) == 0 {
		return false
	}

	for _, got := range Shared(fd) {
		if slices.Contains(set, got) {
			return true
		}
	}

	return false
}

// FirstMatch uses the same AIP priority as update-mask validation.
func FirstMatch(got, set []annotations.FieldBehavior) annotations.FieldBehavior {
	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY, annotations.FieldBehavior_IDENTIFIER,
		annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_REQUIRED} {
		if slices.Contains(got, b) && slices.Contains(set, b) {
			return b
		}
	}

	for _, b := range set {
		if slices.Contains(got, b) {
			return b
		}
	}

	return annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED
}
