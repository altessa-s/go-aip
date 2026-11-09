// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package protopath

import (
	"errors"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/projection"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// wktPackage is the proto package of the well-known types, which are leaves.
const wktPackage protoreflect.FullName = "google.protobuf"

var (
	// ErrDescriptorRequired indicates [New] was called with a nil message descriptor.
	ErrDescriptorRequired = errors.New("message descriptor is required")

	// ErrUnknownField indicates a path segment that names no field of the message it addresses. Errors carrying it also wrap
	// projection.ErrInvalidFieldPath.
	ErrUnknownField = errors.New("unknown field")
)

// Validator checks read mask paths against the fields of a resource message (AIP-161). It is immutable and safe for concurrent
// use.
type Validator struct {
	root protoreflect.MessageDescriptor
	opts options
}

// New returns a [Validator] for paths relative to md, the descriptor of the resource. It fails with [ErrDescriptorRequired] when
// md is nil.
func New(md protoreflect.MessageDescriptor, opts ...Option) (*Validator, error) {
	if md == nil {
		return nil, ErrDescriptorRequired
	}

	return &Validator{root: md, opts: *newOptions(opts...)}, nil
}

// Validate reports whether path addresses a field of the resource. Its signature matches projection.PathValidator, so the method
// value plugs into projection.WithPathValidator and projection.WithPathGrammar. Errors name the offending path and wrap:
//
//   - projection.ErrMaxFieldPathDepthExceeded for a path deeper than [WithMaxDepth];
//   - projection.ErrInvalidFieldPath for an empty or malformed segment, a segment after a repeated field (index access such as
//     "authors.0"), a scalar, an enum or a well-known type, and — together with [ErrUnknownField] — an undeclared field;
//   - projection.ErrUnsupportedPath for AIP-161 syntax the projection grammar does not serve: a segment after a map field
//     (a map key), a "*" wildcard, a backtick-quoted key or a numeric segment on a message.
func (v *Validator) Validate(path string) error {
	if depth := strings.Count(path, ".") + 1; depth > v.opts.maxDepth {
		return coreerrs.Wrapf(projection.ErrMaxFieldPathDepthExceeded,
			"field %q has %d segments (max %d)", path, depth, v.opts.maxDepth)
	}

	segs := strings.Split(path, ".")
	md := v.root

	for i, seg := range segs {
		if err := checkSegment(path, seg); err != nil {
			return err
		}

		fd := v.lookup(md, seg)
		if fd == nil {
			if v.opts.ignoreUnknown {
				return checkSegments(path, segs[i+1:])
			}

			return coreerrs.Wrapf(coreerrs.JoinWrap(projection.ErrInvalidFieldPath, ErrUnknownField),
				"%q: %s has no field %q", path, md.FullName(), seg)
		}

		if i == len(segs)-1 {
			return nil
		}

		next := segs[i+1]

		switch {
		case fd.IsMap():
			if err := checkSegment(path, next); errors.Is(err, projection.ErrInvalidFieldPath) {
				return err
			}

			return coreerrs.Wrapf(projection.ErrUnsupportedPath,
				"%q: map field %q must be the last segment; map key %q is not supported", path, seg, next)
		case fd.IsList():
			return coreerrs.Wrapf(projection.ErrInvalidFieldPath,
				"%q: repeated field %q must be the last segment", path, seg)
		case fd.Message() == nil:
			return coreerrs.Wrapf(projection.ErrInvalidFieldPath,
				"%q: field %q is a %s and has no subfields", path, seg, fd.Kind())
		case fd.Message().FullName().Parent() == wktPackage:
			return coreerrs.Wrapf(projection.ErrInvalidFieldPath,
				"%q: field %q is the well-known type %s and has no subfields", path, seg, fd.Message().FullName())
		}

		md = fd.Message()
	}

	return nil
}

// lookup returns the field of md named seg, by proto name or — with [WithJSONNames] — by JSON name, or nil.
func (v *Validator) lookup(md protoreflect.MessageDescriptor, seg string) protoreflect.FieldDescriptor {
	fields := md.Fields()
	if fd := fields.ByName(protoreflect.Name(seg)); fd != nil {
		return fd
	}

	if v.opts.jsonNames {
		return fields.ByJSONName(seg)
	}

	return nil
}

// checkSegments applies [checkSegment] to every segment.
func checkSegments(path string, segs []string) error {
	for _, seg := range segs {
		if err := checkSegment(path, seg); err != nil {
			return err
		}
	}

	return nil
}

// checkSegment verifies that seg is an identifier ([A-Za-z_][A-Za-z0-9_]*), the same grammar as the default validator of the
// projection package: a wildcard, a backtick-quoted key or a numeric segment is unsupported, anything else is invalid.
func checkSegment(path, seg string) error {
	switch {
	case isIdent(seg):
		return nil
	case seg == "*", strings.Contains(seg, "`"), isNumeric(seg):
		return coreerrs.Wrapf(projection.ErrUnsupportedPath, "%q", path)
	default:
		return coreerrs.Wrapf(projection.ErrInvalidFieldPath, "%q", path)
	}
}

// isIdent reports whether s matches `[A-Za-z_][A-Za-z0-9_]*`.
func isIdent(s string) bool {
	if s == "" {
		return false
	}

	for i := range len(s) {
		c := s[i]
		switch {
		case c == '_', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}

	return true
}

// isNumeric reports whether s is a non-empty all-digit segment.
func isNumeric(s string) bool {
	if s == "" {
		return false
	}

	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}

	return true
}
