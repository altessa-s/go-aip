// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldinfo

import (
	"strconv"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/lru"
)

// subtreeCacheSize bounds the message descriptors whose "holds a formatted
// field" answer a [Checker] caches (see [Checker.HasFormats]).
const subtreeCacheSize = 1024

// Checker validates and normalizes the formatted string fields of messages:
// the AIP-202 formats of google.api.field_info and the custom formats of its
// [Registry]. A Checker is immutable and safe for concurrent use; build one
// per configuration and reuse it.
type Checker struct {
	maxDepth  int
	jsonNames bool
	custom    registrySnapshot
	subtree   *lru.Cache[protoreflect.MessageDescriptor, bool]
}

// builtinSubtree caches [Checker.HasFormats] for checkers without custom
// formats, whose answer depends on the descriptor alone.
var builtinSubtree = mustCache[protoreflect.MessageDescriptor, bool](subtreeCacheSize)

// defaultChecker serves the package-level functions called without options.
var defaultChecker = NewChecker()

// NewChecker returns a checker configured by opts.
func NewChecker(opts ...Option) *Checker {
	o := newOptions(opts...)

	c := &Checker{maxDepth: o.maxDepth, jsonNames: o.jsonNames, custom: o.registry.snapshot(), subtree: builtinSubtree}
	if len(c.custom.fields) > 0 {
		c.subtree = mustCache[protoreflect.MessageDescriptor, bool](subtreeCacheSize)
	}

	return c
}

func checkerFor(opts []Option) *Checker {
	if len(opts) == 0 {
		return defaultChecker
	}

	return NewChecker(opts...)
}

// Validate reports every populated value of msg that does not match the
// format of its field. See [Checker.Validate].
func Validate(msg proto.Message, opts ...Option) error {
	return checkerFor(opts).Validate(msg)
}

// Normalize validates msg and rewrites its formatted values in their
// normalized form. See [Checker.Normalize].
func Normalize(msg proto.Message, opts ...Option) error {
	return checkerFor(opts).Normalize(msg)
}

// Validate reports every populated value of msg that does not match the
// format of its field: a singular string field, every element of a repeated
// string field and every value of a map<_, string> field, in every populated
// message — singular, repeated element and map value — at any depth. An
// unset field is not validated; an explicitly set empty string is, and is
// invalid for every AIP-202 format. A format on a non-string field (AIP-202
// forbids it) and a format this package does not know are ignored.
//
// It returns a *[ViolationError] (matching [ErrInvalidValue]) listing every
// invalid value, or [ErrMaxDepthExceeded] past [WithMaxDepth] nested
// messages. Message types whose descriptor tree holds no formatted field are
// skipped without walking their values.
func (c *Checker) Validate(msg proto.Message) error {
	if msg == nil {
		return nil
	}

	var violations []Violation

	if err := c.walk(msg.ProtoReflect(), "", 0, false, func(m protoreflect.Message, fd protoreflect.FieldDescriptor, path string) {
		violations = c.appendFieldViolations(violations, m, fd, path)
	}); err != nil {
		return err
	}

	if len(violations) > 0 {
		return &ViolationError{Violations: violations}
	}

	return nil
}

// Normalize validates msg as [Checker.Validate] does and, when every value
// is valid, rewrites each formatted value in its normalized form (see
// [NormalizeValue]; a custom format's Normalize, when set, runs after the
// AIP-202 normalization). Use it on input before persisting it, so stored
// values compare as text. On error msg is left untouched.
func (c *Checker) Normalize(msg proto.Message) error {
	if err := c.Validate(msg); err != nil || msg == nil {
		return err
	}

	return c.walk(msg.ProtoReflect(), "", 0, true, func(m protoreflect.Message, fd protoreflect.FieldDescriptor, _ string) {
		c.normalizeField(m, fd)
	})
}

// CheckField returns the violations of the populated string field fd of m,
// whose path is path: one per invalid value, a repeated element indexed and
// a map value keyed under path as [Checker.Validate] does. It returns nil
// when fd is unset, is not a string field, or has no format. It lets a
// caller's own message walk (fieldbehavior's) validate formats in the same
// pass.
func (c *Checker) CheckField(m protoreflect.Message, fd protoreflect.FieldDescriptor, path string) []Violation {
	if m == nil || !m.IsValid() || !c.hasFormat(fd) || !m.Has(fd) {
		return nil
	}

	return c.appendFieldViolations(nil, m, fd, path)
}

// HasFieldFormat reports whether fd is a string field (singular, repeated
// or a map value) carrying a format this checker validates, the fields
// [Checker.CheckField] checks. A caller's walk can ask it before building the
// path of fd.
func (c *Checker) HasFieldFormat(fd protoreflect.FieldDescriptor) bool {
	return c.hasFormat(fd)
}

// HasFormats reports whether any string field reachable from md through
// message fields carries a format this checker validates. A walk skips the
// message types for which it is false. The answer is cached per descriptor
// (see [subtreeCacheSize]).
func (c *Checker) HasFormats(md protoreflect.MessageDescriptor) bool {
	if md == nil {
		return false
	}

	if v, ok := c.subtree.Get(md); ok {
		return v
	}

	v := c.subtreeHasFormats(md, map[protoreflect.MessageDescriptor]bool{})
	c.subtree.Put(md, v)

	return v
}

func (c *Checker) subtreeHasFormats(md protoreflect.MessageDescriptor, visited map[protoreflect.MessageDescriptor]bool) bool {
	if md == nil || visited[md] {
		return false
	}

	visited[md] = true

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if c.hasFormat(fd) {
			return true
		}

		if sub := messageOf(fd); sub != nil && c.subtreeHasFormats(sub, visited) {
			return true
		}
	}

	return false
}

// hasFormat reports whether fd is a string field with a known AIP-202 or a
// custom format.
func (c *Checker) hasFormat(fd protoreflect.FieldDescriptor) bool {
	if !isStringValue(fd) {
		return false
	}

	if Known(FormatOf(fd)) {
		return true
	}

	_, _, ok := c.custom.custom(fd)

	return ok
}

// messageOf returns the message type a field's values hold, the map value
// type for a map, or nil for scalar values.
func messageOf(fd protoreflect.FieldDescriptor) protoreflect.MessageDescriptor {
	if fd.IsMap() {
		return fd.MapValue().Message()
	}

	return fd.Message()
}

// visitFunc is called for every populated formatted field of a walk.
type visitFunc func(m protoreflect.Message, fd protoreflect.FieldDescriptor, path string)

// walk calls visit for every populated formatted field of m and descends its
// populated messages. mutable selects mutation-capable views of nested
// values.
func (c *Checker) walk(m protoreflect.Message, prefix string, depth int, mutable bool, visit visitFunc) error {
	if m == nil || !m.IsValid() || !c.HasFormats(m.Descriptor()) {
		return nil
	}

	if c.maxDepth > 0 && depth > c.maxDepth {
		return ErrMaxDepthExceeded
	}

	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if !m.Has(fd) {
			continue
		}

		path := behavior.JoinPath(prefix, c.segment(fd))

		if c.hasFormat(fd) {
			visit(m, fd, path)
			continue
		}

		if err := c.descend(m, fd, path, depth+1, mutable, visit); err != nil {
			return err
		}
	}

	return nil
}

func (c *Checker) descend(m protoreflect.Message, fd protoreflect.FieldDescriptor, path string, depth int, mutable bool, visit visitFunc) error {
	sub := messageOf(fd)
	if sub == nil || !c.HasFormats(sub) {
		return nil
	}

	v := m.Get(fd)
	if mutable {
		v = m.Mutable(fd)
	}

	switch {
	case fd.IsList():
		return behavior.ForEachMessageInList(v.List(), func(i int, elem protoreflect.Message) error {
			return c.walk(elem, behavior.JoinPath(path, strconv.Itoa(i)), depth, mutable, visit)
		})
	case fd.IsMap():
		mm := v.Map()
		for _, k := range behavior.SortedMapKeys(mm) {
			if err := c.walk(mm.Get(k).Message(), behavior.JoinPath(path, behavior.QuoteSegment(k.String())), depth, mutable, visit); err != nil {
				return err
			}
		}

		return nil
	default:
		return c.walk(v.Message(), path, depth, mutable, visit)
	}
}

// segment is the path segment of fd: its proto name, or its JSON name under
// [WithJSONNames].
func (c *Checker) segment(fd protoreflect.FieldDescriptor) string {
	if c.jsonNames {
		return fd.JSONName()
	}

	return string(fd.Name())
}

// appendFieldViolations appends the violations of the values of the
// populated formatted string field fd of m.
func (c *Checker) appendFieldViolations(dst []Violation, m protoreflect.Message, fd protoreflect.FieldDescriptor, path string) []Violation {
	v := m.Get(fd)

	switch {
	case fd.IsList():
		list := v.List()
		for i := range list.Len() {
			dst = c.appendValueViolation(dst, fd, behavior.JoinPath(path, strconv.Itoa(i)), list.Get(i).String())
		}
	case fd.IsMap():
		mm := v.Map()
		for _, k := range behavior.SortedMapKeys(mm) {
			dst = c.appendValueViolation(dst, fd, behavior.JoinPath(path, behavior.QuoteSegment(k.String())), mm.Get(k).String())
		}
	default:
		dst = c.appendValueViolation(dst, fd, path, v.String())
	}

	return dst
}

func (c *Checker) appendValueViolation(dst []Violation, fd protoreflect.FieldDescriptor, path, s string) []Violation {
	if ve := c.validateValue(fd, s); ve != nil {
		return append(dst, Violation{Path: path, Format: ve.Format, Reason: ve.Reason})
	}

	return dst
}

// validateValue checks s against the AIP-202 format of fd, then its custom
// format, which sees the normalized value. It allocates only for a custom
// format, or a violation.
func (c *Checker) validateValue(fd protoreflect.FieldDescriptor, s string) *ValueError {
	name, cf, custom := c.custom.custom(fd)

	if f := FormatOf(fd); Known(f) {
		if ve := validateKnown(f, s); ve != nil {
			return ve
		}

		if custom {
			s, _ = NormalizeValue(f, s)
		}
	}

	if custom && cf.Validate(s) != nil {
		return &ValueError{Format: name, Reason: cf.Reason}
	}

	return nil
}

// normalizeValue validates s against the AIP-202 format of fd, then its
// custom format, and returns the normalized value or a *[ValueError].
func (c *Checker) normalizeValue(fd protoreflect.FieldDescriptor, s string) (string, error) {
	if f := FormatOf(fd); Known(f) {
		n, err := NormalizeValue(f, s)
		if err != nil {
			return "", err
		}

		s = n
	}

	name, cf, ok := c.custom.custom(fd)
	if !ok {
		return s, nil
	}

	if err := cf.Validate(s); err != nil {
		return "", &ValueError{Format: name, Reason: cf.Reason}
	}

	if cf.Normalize != nil {
		s = cf.Normalize(s)
	}

	return s, nil
}

// normalizeField rewrites the values of the populated, validated formatted
// field fd of m in their normalized form.
func (c *Checker) normalizeField(m protoreflect.Message, fd protoreflect.FieldDescriptor) {
	switch {
	case fd.IsList():
		list := m.Mutable(fd).List()
		for i := range list.Len() {
			if n, changed := c.normalized(fd, list.Get(i).String()); changed {
				list.Set(i, protoreflect.ValueOfString(n))
			}
		}
	case fd.IsMap():
		mm := m.Mutable(fd).Map()
		for _, k := range behavior.SortedMapKeys(mm) {
			if n, changed := c.normalized(fd, mm.Get(k).String()); changed {
				mm.Set(k, protoreflect.ValueOfString(n))
			}
		}
	default:
		if n, changed := c.normalized(fd, m.Get(fd).String()); changed {
			m.Set(fd, protoreflect.ValueOfString(n))
		}
	}
}

// normalized returns the normalized form of the validated value s and
// whether it differs from s.
func (c *Checker) normalized(fd protoreflect.FieldDescriptor, s string) (string, bool) {
	n, err := c.normalizeValue(fd, s)
	if err != nil {
		return s, false
	}

	return n, n != s
}
