// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldinfo

import (
	"fmt"
	"maps"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// CustomFormat is a format the caller defines for values AIP-202 has no
// format for, such as an email address: google.api.FieldInfo.Format has no
// email value, and this package does not invent an annotation for one.
type CustomFormat struct {
	// Validate returns nil for a valid value and an error otherwise. The
	// error itself is not reported: a [Violation] carries Reason, so a
	// validator cannot leak the value into an API error. Required.
	Validate func(value string) error
	// Normalize returns the normalized form of a valid value. Optional: nil
	// keeps the value as is.
	Normalize func(value string) string
	// Reason says what a valid value looks like; it is the Reason of a
	// [Violation]. Empty uses "must be a valid <name>".
	Reason string
}

// Registry maps caller-chosen names to custom formats and fields to those
// names. The zero value is an empty registry. Fill it before use and pass it
// with [WithRegistry]: [NewChecker] copies it, so later changes do not affect
// a constructed [Checker]. A Registry is not safe for concurrent mutation.
type Registry struct {
	formats map[string]CustomFormat
	fields  map[protoreflect.FullName]string
}

// Register adds the custom format f under name. It returns an error matching
// [ErrInvalidRegistry] for an empty name, a nil f.Validate or a name already
// registered.
func (r *Registry) Register(name string, f CustomFormat) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: empty format name", ErrInvalidRegistry)
	case f.Validate == nil:
		return fmt.Errorf("%w: format %q has no validator", ErrInvalidRegistry, name)
	}

	if _, dup := r.formats[name]; dup {
		return fmt.Errorf("%w: format %q already registered", ErrInvalidRegistry, name)
	}

	if f.Reason == "" {
		f.Reason = "must be a valid " + name
	}

	if r.formats == nil {
		r.formats = map[string]CustomFormat{}
	}

	r.formats[name] = f

	return nil
}

// Assign applies the registered format name to the string field field (its
// full name, "library.v1.Author.email"). A repeated field applies it to
// every element, a map field to every value. A field that also carries an
// AIP-202 format must satisfy both. It returns an error matching
// [ErrInvalidRegistry] for an unregistered name or a field already assigned.
func (r *Registry) Assign(field protoreflect.FullName, name string) error {
	if _, ok := r.formats[name]; !ok {
		return fmt.Errorf("%w: format %q is not registered", ErrInvalidRegistry, name)
	}

	if prev, dup := r.fields[field]; dup {
		return fmt.Errorf("%w: field %s already has format %q", ErrInvalidRegistry, field, prev)
	}

	if r.fields == nil {
		r.fields = map[protoreflect.FullName]string{}
	}

	r.fields[field] = name

	return nil
}

// registrySnapshot is the immutable copy of a [Registry] a [Checker] uses.
type registrySnapshot struct {
	formats map[string]CustomFormat
	fields  map[protoreflect.FullName]string
}

func (r *Registry) snapshot() registrySnapshot {
	if r == nil {
		return registrySnapshot{}
	}

	return registrySnapshot{formats: maps.Clone(r.formats), fields: maps.Clone(r.fields)}
}

// custom returns the name and custom format assigned to fd.
func (s registrySnapshot) custom(fd protoreflect.FieldDescriptor) (string, CustomFormat, bool) {
	if len(s.fields) == 0 {
		return "", CustomFormat{}, false
	}

	name, ok := s.fields[fd.FullName()]
	if !ok {
		return "", CustomFormat{}, false
	}

	return name, s.formats[name], true
}
