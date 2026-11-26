// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"slices"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/maskpath"
)

// FieldPolicy describes the effective behavior of one resource field.
type FieldPolicy struct {
	Identity   bool // An identifier or configured root identity field, unless output-only.
	OutputOnly bool // The resolver marks the field OUTPUT_ONLY.
	WriteOnly  bool // A root INPUT_ONLY field that is neither identity, output-only nor immutable.
	Immutable  bool // The resolver or a root-field option marks the field immutable.
	Required   bool // The resolver marks the field REQUIRED and it is neither identity, output-only nor write-only.
}

// FieldPolicy returns the policy of the last field in a descriptor route
// starting at the resource. Build the route from the spec's schema descriptors.
// A list or map of messages leads to its element or value type. An extension
// must extend the exact message descriptor reached by the route.
//
// NewSpec snapshots resolver results for the declared root fields. All spec
// operations use those results, including when the same descriptors recur at
// depth. Other nested fields and extensions are resolved on demand.
//
// The result is independent of mask selection and [WithAllowedPaths]. Fields
// inside an atomic message remain inspectable. An empty or foreign route
// (including independently built equivalent dynamic descriptors), or a nil
// spec, returns [ErrMessageMismatch].
// FieldPolicy is safe for concurrent use.
func (s *Spec) FieldPolicy(route ...protoreflect.FieldDescriptor) (FieldPolicy, error) {
	if s == nil || len(route) == 0 {
		return FieldPolicy{}, ErrMessageMismatch
	}

	md := s.desc
	for i, fd := range route {
		if md == nil || fd == nil || fd.ContainingMessage() != md ||
			(!fd.IsExtension() && md.Fields().ByNumber(fd.Number()) != fd) {
			return FieldPolicy{}, ErrMessageMismatch
		}

		if i+1 < len(route) {
			md = maskpath.ValueMessage(fd)
		}
	}

	fd := route[len(route)-1]

	var f fieldClassification

	if len(route) == 1 {
		f = s.rootClassification(fd)
	} else {
		f = s.opts.classifyField(fd, false)
	}

	identity := f.isIdentity() && !f.outputOnly
	writeOnly := f.isWriteOnly()

	return FieldPolicy{
		Identity:   identity,
		OutputOnly: f.outputOnly,
		WriteOnly:  writeOnly,
		Immutable:  f.immutable,
		Required:   f.required && !identity && !f.outputOnly && !writeOnly,
	}, nil
}

type fieldClassification struct {
	identifier, namedIdentity, outputOnly, immutable, inputOnly, required, root bool
}

// classifyField interprets resolver annotations and adds name-based policy
// only at the root. Its callers decide which behaviors apply to their operation.
func (o *options) classifyField(fd protoreflect.FieldDescriptor, root bool) fieldClassification {
	return o.classifyBehaviors(fd, root, o.behaviors(fd))
}

func (o *options) classifyBehaviors(fd protoreflect.FieldDescriptor, root bool, behaviors []annotations.FieldBehavior) fieldClassification {
	f := fieldClassification{root: root}

	for _, b := range behaviors {
		switch b {
		case annotations.FieldBehavior_IDENTIFIER:
			f.identifier = true
		case annotations.FieldBehavior_OUTPUT_ONLY:
			f.outputOnly = true
		case annotations.FieldBehavior_IMMUTABLE:
			f.immutable = true
		case annotations.FieldBehavior_INPUT_ONLY:
			f.inputOnly = true
		case annotations.FieldBehavior_REQUIRED:
			f.required = true
		default:
			// Other annotations do not affect update policy.
		}
	}

	if root {
		name := string(fd.Name())
		f.namedIdentity = slices.Contains(o.identityFields, name)
		f.immutable = f.immutable || slices.Contains(o.immutableFields, name)
	}

	return f
}

func (f fieldClassification) isIdentity() bool { return f.identifier || f.namedIdentity }

func (f fieldClassification) isWriteOnly() bool {
	return f.root && f.inputOnly && !f.isIdentity() && !f.outputOnly && !f.immutable
}

func (f fieldClassification) managedBehavior() annotations.FieldBehavior {
	switch {
	case f.outputOnly:
		return annotations.FieldBehavior_OUTPUT_ONLY
	case f.isIdentity():
		return annotations.FieldBehavior_IDENTIFIER
	case f.immutable:
		return annotations.FieldBehavior_IMMUTABLE
	default:
		return annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED
	}
}

func (f fieldClassification) maskClass() maskClass {
	switch {
	// Keep identity precedence for mask bookkeeping, including implied skips
	// and oneof guards. The behavior of the field itself gives OUTPUT_ONLY priority.
	case f.isIdentity():
		return classIdentity
	case f.outputOnly:
		return classOutputOnly
	case f.immutable:
		return classImmutable
	case f.isWriteOnly():
		return classSecret
	default:
		return classMutable
	}
}
