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

	"github.com/altessa-s/go-aip/fieldbehavior"
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
// and every other top-level field is mutable. A nested INPUT_ONLY field is an ordinary
// leaf of its parent (see [Spec.Plan]). Use [Spec.FieldPolicy] to inspect
// root and nested fields. A Spec is immutable and safe for concurrent use.
type Spec struct {
	opts       *options
	allowed    *allowedMaskPaths
	desc       protoreflect.MessageDescriptor
	order      []protoreflect.FieldDescriptor // top-level fields in declaration order
	rootFields *coremaps.ImmutableMap[protoreflect.FieldDescriptor, rootFieldPolicy]
	skip       []string // never implied: identity, secrets, immutable
}

// rootFieldPolicy retains the full classification and the ordered behavior
// list used by the shared validation and merge walks.
type rootFieldPolicy struct {
	classification fieldClassification
	behaviors      []annotations.FieldBehavior
}

// NewSpec returns the mask specification of resource's message type;
// options replace identity names, atomic-message policy and the error field.
// [WithImmutableFields] adds create-only fields. [WithAllowedPaths] is compiled
// against the descriptor here. NewSpec resolves each declared root field
// once. Nil resources return [ErrMessageMismatch].
func NewSpec(resource proto.Message, opts ...Option) (*Spec, error) {
	if !validMessage(resource) {
		return nil, ErrMessageMismatch
	}

	o := newOptions(opts...)
	desc := resource.ProtoReflect().Descriptor()
	s := &Spec{desc: desc, opts: o}

	fields := desc.Fields()

	rootFields := make(map[protoreflect.FieldDescriptor]rootFieldPolicy, fields.Len())
	for i := range fields.Len() {
		fd := fields.Get(i)
		behaviors := slices.Clone(o.behaviors(fd))
		classification := o.classifyBehaviors(fd, true, behaviors)
		rootFields[fd] = rootFieldPolicy{classification: classification, behaviors: behaviors}
		s.order = append(s.order, fd)

		switch classification.maskClass() {
		case classIdentity, classImmutable, classSecret:
			s.skip = append(s.skip, string(fd.Name()))
		case classMutable, classOutputOnly:
		}
	}

	snapshot := coremaps.NewImmutableMap(rootFields)
	s.rootFields = snapshot
	resolver := o.resolver
	o.resolver = fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if p, ok := snapshot.Get(fd); ok {
			return p.behaviors
		}

		return resolveBehaviors(resolver, fd)
	})

	if o.allowedPaths != nil {
		allowed, err := s.compileAllowedPaths(o.allowedPaths)
		if err != nil {
			return nil, err
		}

		s.allowed = allowed
		o.allowedPaths = nil
	}

	return s, nil
}

func (s *Spec) class(name string) maskClass {
	fd := s.desc.Fields().ByName(protoreflect.Name(name))
	if fd == nil {
		return 0
	}

	return s.rootClassification(fd).maskClass()
}

func (s *Spec) rootClassification(fd protoreflect.FieldDescriptor) fieldClassification {
	if p, ok := s.rootFields.Get(fd); ok {
		return p.classification
	}

	return s.opts.classifyField(fd, true)
}

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
