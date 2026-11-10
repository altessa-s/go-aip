// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package extract

import (
	"errors"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

const (
	// DefaultUpdateMaskFieldName is the AIP-134 conventional name for the
	// update mask field on an Update* request message. Default lookup tries
	// the declared name "updateMask" only when "update_mask" is not declared.
	// [WithMaskField] overrides this rule with an exact declared-name lookup.
	DefaultUpdateMaskFieldName = "update_mask"

	// DefaultReadMaskFieldName is the AIP-157 conventional name for the
	// read mask field on a Get/List/Search request message. Default lookup tries
	// the declared name "readMask" only when "read_mask" is not declared.
	// [WithMaskField] overrides this rule with an exact declared-name lookup.
	DefaultReadMaskFieldName = "read_mask"

	// fieldMaskMessageFullName is the protobuf full name of the wire-level
	// field mask message.
	fieldMaskMessageFullName protoreflect.FullName = "google.protobuf.FieldMask"
)

// ErrFieldNotSettable is returned by [SetUpdateMask] when the request message
// does not expose a settable singular google.protobuf.FieldMask under the
// selected declared name.
var ErrFieldNotSettable = errors.New("fieldmask: update_mask field is not present or has a wrong type")

// UpdateMask locates the AIP-134 update_mask field on req and the
// sibling resource sub-message updated by that mask. It returns (mask,
// resource, true) when both are present and the message shape matches the
// convention.
//
// Selection rules:
//
//   - The mask field is a singular [fieldmaskpb.FieldMask] declared as
//     "update_mask", or "updateMask" only when "update_mask" is not declared.
//     [WithMaskField] selects an exact declared name without fallback.
//   - The resource is the field named via [WithResourceField], or, if no
//     override is set, the only non-mask message field declared on req.
//     Repeated and map message fields are skipped. Ambiguous requests do not
//     match the convention and should register an extractor.
//   - Both fields must be populated on req; an unset mask or resource yields
//     ok=false so the caller can fall through to a passthrough.
func UpdateMask(req proto.Message, opts ...Option) (mask *fieldmaskpb.FieldMask, resource proto.Message, ok bool) {
	o := newExtractOptions(opts...)

	maskMsg, prf, maskFD, found := lookupFieldMask(req, DefaultUpdateMaskFieldName, o.maskField, true)
	if !found || maskMsg == nil {
		return nil, nil, false
	}

	resFD := resolveResourceField(prf.Descriptor().Fields(), maskFD, o.resourceField)
	if resFD == nil || !prf.Has(resFD) {
		return maskMsg, nil, false
	}

	return maskMsg, prf.Get(resFD).Message().Interface(), true
}

// IsCanonicalUpdateRequest reports whether req has the canonical AIP-134
// shape: a singular google.protobuf.FieldMask declared as update_mask, or
// updateMask only when update_mask is not declared, and exactly one other
// singular non-mask message field to carry the resource. [WithMaskField]
// selects an exact declared name without fallback. The mask and resource
// need not be populated.
func IsCanonicalUpdateRequest(req proto.Message, opts ...Option) bool {
	if req == nil || !req.ProtoReflect().IsValid() {
		return false
	}

	o := newExtractOptions(opts...)
	fields := req.ProtoReflect().Descriptor().Fields()

	maskFD := resolveMaskField(fields, DefaultUpdateMaskFieldName, o.maskField)
	if !isFieldMask(maskFD) {
		return false
	}

	count := 0

	for i := range fields.Len() {
		fd := fields.Get(i)
		if fd == maskFD || fd.Kind() != protoreflect.MessageKind || fd.IsList() || fd.IsMap() || isFieldMask(fd) {
			continue
		}

		count++
	}

	return count == 1
}

// ReadMask locates the AIP-157 read_mask field on req. Returns
// (mask, true) when the field is a populated singular [fieldmaskpb.FieldMask].
// It selects the declared name "read_mask", or "readMask" only when
// "read_mask" is not declared. [WithMaskField] selects an exact declared
// name without fallback.
func ReadMask(req proto.Message, opts ...Option) (mask *fieldmaskpb.FieldMask, ok bool) {
	o := newExtractOptions(opts...)

	maskMsg, _, _, found := lookupFieldMask(req, DefaultReadMaskFieldName, o.maskField, true)
	if !found || maskMsg == nil {
		return nil, false
	}

	return maskMsg, true
}

// SetUpdateMask writes mask back onto req's selected update-mask field. Used by the
// fieldmask gRPC interceptor after [github.com/altessa-s/go-aip/fieldmask/update.Apply] removes
// OUTPUT_ONLY entries so the handler sees a coherent (mask, resource) pair.
// Returns [ErrFieldNotSettable] when the selected field is absent or is not
// a singular [fieldmaskpb.FieldMask].
// The field must be singular and declared as "update_mask", or "updateMask"
// only when "update_mask" is not declared. [WithMaskField] selects an exact
// declared name without fallback. An unset mask field can be written;
// passing nil clears the selected field.
func SetUpdateMask(req proto.Message, mask *fieldmaskpb.FieldMask, opts ...Option) error {
	o := newExtractOptions(opts...)

	_, prf, fd, found := lookupFieldMask(req, DefaultUpdateMaskFieldName, o.maskField, false)
	if !found {
		return ErrFieldNotSettable
	}

	if mask == nil {
		prf.Clear(fd)
		return nil
	}

	// Preserve the generated representation when the request uses the standard
	// descriptor; dynamic schemas with distinct descriptors use their own value.
	if fd.Message() == mask.ProtoReflect().Descriptor() {
		prf.Set(fd, protoreflect.ValueOfMessage(proto.Clone(mask).ProtoReflect()))
		return nil
	}

	value := prf.NewField(fd).Message()
	pathsFD := value.Descriptor().Fields().ByName("paths")

	paths := value.Mutable(pathsFD).List()
	for _, path := range mask.GetPaths() {
		paths.Append(protoreflect.ValueOfString(path))
	}

	prf.Set(fd, protoreflect.ValueOfMessage(value))

	return nil
}

// lookupFieldMask resolves a [fieldmaskpb.FieldMask] field on req using
// [resolveMaskField] for the default name or exact override. It validates
// the request and mask value for [UpdateMask], [ReadMask], and [SetUpdateMask].
//
// Returns found=true only when req is non-nil, reflects to a valid message,
// and exposes a singular google.protobuf.FieldMask field under the selected
// name. When mustBeSet is true the field must additionally be populated on req — used
// by the extractor paths so an unset mask yields ok=false; SetUpdateMask
// passes mustBeSet=false because it needs the descriptor even to clear the
// field. The returned mask copies paths through reflection, including dynamic messages.
func lookupFieldMask(req proto.Message, defaultName, override string, mustBeSet bool) (
	mask *fieldmaskpb.FieldMask,
	prf protoreflect.Message,
	fd protoreflect.FieldDescriptor,
	found bool,
) {
	if req == nil {
		return nil, nil, nil, false
	}

	prf = req.ProtoReflect()
	if !prf.IsValid() {
		return nil, nil, nil, false
	}

	fd = resolveMaskField(prf.Descriptor().Fields(), defaultName, override)
	if !isFieldMask(fd) {
		return nil, nil, nil, false
	}

	if !prf.Has(fd) {
		if mustBeSet {
			return nil, nil, nil, false
		}

		return nil, prf, fd, true
	}

	value := prf.Get(fd).Message()

	pathsFD := value.Descriptor().Fields().ByName("paths")
	if pathsFD == nil || !pathsFD.IsList() || pathsFD.Kind() != protoreflect.StringKind {
		return nil, nil, nil, false
	}

	paths := value.Get(pathsFD).List()

	mask = &fieldmaskpb.FieldMask{Paths: make([]string, paths.Len())}
	for i := range paths.Len() {
		mask.Paths[i] = paths.Get(i).String()
	}

	return mask, prf, fd, true
}

// resolveMaskField selects an exact override, or the conventional declared
// name followed by its lowerCamelCase spelling only if that name is absent.
// Selection precedes type and presence checks, so a declared snake_case field
// always wins even if it is unset or has the wrong type.
func resolveMaskField(fields protoreflect.FieldDescriptors, defaultName, override string) protoreflect.FieldDescriptor {
	if override != "" {
		return fields.ByName(protoreflect.Name(override))
	}

	if fd := fields.ByName(protoreflect.Name(defaultName)); fd != nil {
		return fd
	}

	switch defaultName {
	case DefaultUpdateMaskFieldName:
		return fields.ByName("updateMask")
	case DefaultReadMaskFieldName:
		return fields.ByName("readMask")
	default:
		return nil
	}
}

// isFieldMask reports whether fd is a singular message field whose descriptor
// is google.protobuf.FieldMask.
func isFieldMask(fd protoreflect.FieldDescriptor) bool {
	if fd == nil || fd.Kind() != protoreflect.MessageKind || fd.IsList() || fd.IsMap() {
		return false
	}

	md := fd.Message()
	if md == nil {
		return false
	}

	if md.FullName() != fieldMaskMessageFullName {
		return false
	}

	paths := md.Fields().ByName("paths")

	return paths != nil && paths.IsList() && paths.Kind() == protoreflect.StringKind
}

// resolveResourceField picks the resource carrier field. When override is
// non-empty the descriptor is looked up by name; otherwise exactly one
// non-mask, non-repeated, non-map message field must exist.
func resolveResourceField(fields protoreflect.FieldDescriptors, maskFD protoreflect.FieldDescriptor, override string) protoreflect.FieldDescriptor {
	if override != "" {
		fd := fields.ByName(protoreflect.Name(override))
		if fd == nil || fd.Kind() != protoreflect.MessageKind || fd.IsList() || fd.IsMap() {
			return nil
		}

		return fd
	}

	var resource protoreflect.FieldDescriptor

	for i := range fields.Len() {
		fd := fields.Get(i)
		if fd == maskFD {
			continue
		}

		if fd.Kind() != protoreflect.MessageKind || fd.IsList() || fd.IsMap() {
			continue
		}

		if fd.Message() != nil && fd.Message().FullName() == fieldMaskMessageFullName {
			continue
		}

		if resource != nil {
			return nil
		}

		resource = fd
	}

	return resource
}
