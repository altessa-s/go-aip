// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldbehavior

import (
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/behavior"
)

// Resolver supplies field annotations. Implementations must support concurrent
// calls when shared; callers may inject a bounded, instance-owned cache here.
type Resolver interface {
	Resolve(protoreflect.FieldDescriptor) []annotations.FieldBehavior
}

// AnnotationResolver reads google.api.field_behavior. The decoded annotation
// is cached per field descriptor in a bounded, process-wide cache (4096
// descriptors, least recently used evicted), so dynamic descriptors are never
// retained without limit.
type AnnotationResolver struct{}

// Resolve returns an owned copy of the annotations.
func (AnnotationResolver) Resolve(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
	return behavior.Get(fd)
}

// ResolverFunc adapts a function to Resolver.
type ResolverFunc func(protoreflect.FieldDescriptor) []annotations.FieldBehavior

// Resolve returns the behaviors supplied by f.
func (f ResolverFunc) Resolve(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
	return f(fd)
}

// resolveFunc returns the annotation reader of the walks. The default resolver
// reads the cached annotations without copying them: the walks never modify
// the slice.
func (o *options) resolveFunc() behavior.Resolve {
	if _, standard := o.resolver.(AnnotationResolver); standard {
		return behavior.Shared
	}

	return o.resolver.Resolve
}
