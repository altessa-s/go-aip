// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"errors"
	"slices"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/maskmerge"

	coremaps "github.com/altessa-s/go-atlas-core/collections/maps"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// DefaultUpdateMaskField is the request field an update mask arrives in (AIP-134),
// the field [Spec.Plan] names for refusals.
const DefaultUpdateMaskField = "updateMask"

// Identity fields of a versioned resource (AIP-154, AIP-203): never
// updatable, never implied.
const (
	fieldID   = "id"
	fieldEtag = "etag"
)

// maskClass is how a top-level field of a resource is treated by an update
// mask.
type maskClass int

const (
	classMutable    maskClass = iota + 1 // applied from the request
	classSecret                          // INPUT_ONLY write-only value
	classImmutable                       // set on create, never changed
	classIdentity                        // id, etag, IDENTIFIER
	classOutputOnly                      // server-managed
)

// Spec describes the update-mask paths of a resource message (AIP-134),
// derived from its descriptor: id, etag and IDENTIFIER fields are identity,
// OUTPUT_ONLY fields are server-managed, IMMUTABLE fields and the fields
// named on construction are immutable (an INPUT_ONLY one included: a
// create-only credential), other INPUT_ONLY fields are write-only secrets,
// and every other top-level field is mutable. Only top-level fields are
// classified: a nested INPUT_ONLY field is an ordinary leaf of its mutable
// parent (see [Spec.Plan]). It is immutable and
// safe for concurrent use.
type Spec struct {
	opts    *options
	desc    protoreflect.MessageDescriptor
	order   []protoreflect.FieldDescriptor // top-level fields in declaration order
	classes *coremaps.ImmutableMap[string, maskClass]
	skip    []string // never implied: identity, secrets, immutable
}

// NewSpec returns the mask specification of resource's message type;
// options replace identity names, atomic-message policy and the error field.
// WithImmutableFields adds create-only fields. Nil resources return ErrMessageMismatch.
func NewSpec(resource proto.Message, opts ...Option) (*Spec, error) {
	if !validMessage(resource) {
		return nil, ErrMessageMismatch
	}

	o := newOptions(opts...)
	desc := resource.ProtoReflect().Descriptor()
	s := &Spec{desc: desc, opts: o}
	classes := map[string]maskClass{}

	fields := desc.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		name := string(fd.Name())
		behaviors := o.behaviors(fd)
		s.order = append(s.order, fd)

		switch {
		case slices.Contains(o.identityFields, name) || slices.Contains(behaviors, annotations.FieldBehavior_IDENTIFIER):
			classes[name] = classIdentity
			s.skip = append(s.skip, name)
		case slices.Contains(behaviors, annotations.FieldBehavior_OUTPUT_ONLY):
			classes[name] = classOutputOnly
		case slices.Contains(behaviors, annotations.FieldBehavior_IMMUTABLE) || slices.Contains(o.immutableFields, name):
			// Before INPUT_ONLY: a create-only credential is immutable, never a
			// secret an update may set or clear.
			classes[name] = classImmutable
			s.skip = append(s.skip, name)
		case slices.Contains(behaviors, annotations.FieldBehavior_INPUT_ONLY):
			classes[name] = classSecret
			s.skip = append(s.skip, name)
		default:
			classes[name] = classMutable
		}
	}

	s.classes = coremaps.NewImmutableMap(classes)

	return s, nil
}

func (s *Spec) class(name string) maskClass { value, _ := s.classes.Get(name); return value }

// fieldError applies the request's configured mask field without changing
// the cause: the field of the offending path ("updateMask.paths[2]") when
// the cause locates one, the bare mask field otherwise.
func (s *Spec) fieldError(err error) error {
	if e, ok := errors.AsType[*coreerrs.FieldError](err); ok {
		cloned := *e
		cloned.Field = s.opts.updateMaskFieldName

		if mp, ok := errors.AsType[*MaskPathError](e.Err); ok {
			cloned.Field = maskPathField(s.opts.updateMaskFieldName, mp.Index)
		}

		return &cloned
	}

	return err
}

// DefaultAtomicMessage treats standard protobuf and google.type value messages as leaves,
// except the dynamic Struct, Value and ListValue messages, whose JSON a path addresses by
// key and "*" (see [WithStrictDynamicPaths]).
func DefaultAtomicMessage(md protoreflect.MessageDescriptor) bool {
	return maskmerge.DefaultAtomicMessage(md)
}
