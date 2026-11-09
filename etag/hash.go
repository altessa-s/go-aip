// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package etag

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldbehavior"
)

// FromMessage returns the content tag of a resource: the SHA-256 digest of
// its deterministic binary encoding, unpadded base64url (43 bytes) by
// default or hex with [WithHexEncoding](true). m is not modified.
//
// The top-level etag field is always left out, so stamping the tag into
// the resource does not change it. [WithHashFields] limits the hash to a
// field subset, [WithExcludeFields] and [WithExcludeBehaviors] leave fields
// out (an OUTPUT_ONLY update time a store rewrites on every save, say).
// The tag is strong unless [WithWeak](true): mark it weak when exclusions
// make equal tags mean semantically, not byte-for-byte, equal resources.
//
// Deterministic encoding is stable for one build of one message type, not
// across protobuf runtimes or languages: compare tags a single service
// computes, or store them with the resource. A field path that does not name
// a field is [ErrNoField]; a nil message is [ErrNilMessage].
func FromMessage(m proto.Message, opts ...HashOption) (Tag, error) {
	if m == nil || !m.ProtoReflect().IsValid() {
		return Tag{}, ErrNilMessage
	}

	o := newHashOptions(opts...)
	md := m.ProtoReflect().Descriptor()

	include, err := resolvePaths(md, o.hashFields)
	if err != nil {
		return Tag{}, err
	}

	exclude, err := resolvePaths(md, o.excludeFields)
	if err != nil {
		return Tag{}, err
	}

	c := proto.Clone(m).ProtoReflect()
	if len(include) > 0 {
		subset := c.New()
		for _, fds := range include {
			copyPath(c, subset, fds)
		}

		c = subset
	}

	if fd := md.Fields().ByName(FieldName); fd != nil {
		c.Clear(fd)
	}

	for _, fds := range exclude {
		clearPath(c, fds)
	}

	if len(o.excludeBehaviors) > 0 {
		err = fieldbehavior.Strip(c.Interface(),
			fieldbehavior.WithBehaviors(o.excludeBehaviors...), fieldbehavior.WithMaxDepth(math.MaxInt))
		if err != nil {
			return Tag{}, fmt.Errorf("etag: exclude behaviors: %w", err)
		}
	}

	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(c.Interface())
	if err != nil {
		return Tag{}, fmt.Errorf("etag: marshal: %w", err)
	}

	sum := sha256.Sum256(b)

	value := base64.RawURLEncoding.EncodeToString(sum[:])
	if o.hex {
		value = hex.EncodeToString(sum[:])
	}

	if o.weak {
		return Weak(value), nil
	}

	return Strong(value), nil
}

// resolvePath returns the field descriptors of a dotted path: every segment
// but the last must name a singular message field.
func resolvePath(md protoreflect.MessageDescriptor, p string) ([]protoreflect.FieldDescriptor, error) {
	segs := strings.Split(p, ".")
	fds := make([]protoreflect.FieldDescriptor, 0, len(segs))

	for i, seg := range segs {
		fd := md.Fields().ByName(protoreflect.Name(seg))
		if fd == nil {
			return nil, fmt.Errorf("etag: field path %q: %w", p, ErrNoField)
		}

		fds = append(fds, fd)

		if i == len(segs)-1 {
			break
		}

		if fd.Message() == nil || fd.IsList() || fd.IsMap() {
			return nil, fmt.Errorf("etag: field path %q: %q is not a singular message: %w", p, seg, ErrNoField)
		}

		md = fd.Message()
	}

	return fds, nil
}

// resolvePaths resolves every path of paths.
func resolvePaths(md protoreflect.MessageDescriptor, paths []string) ([][]protoreflect.FieldDescriptor, error) {
	out := make([][]protoreflect.FieldDescriptor, 0, len(paths))
	for _, p := range paths {
		fds, err := resolvePath(md, p)
		if err != nil {
			return nil, err
		}

		out = append(out, fds)
	}

	return out, nil
}

// copyPath copies the value at fds from src into dst, creating the
// intermediate messages of dst; an unset value copies nothing.
func copyPath(src, dst protoreflect.Message, fds []protoreflect.FieldDescriptor) {
	last := len(fds) - 1
	for _, fd := range fds[:last] {
		if !src.Has(fd) {
			return
		}

		src, dst = src.Get(fd).Message(), dst.Mutable(fd).Message()
	}

	if src.Has(fds[last]) {
		dst.Set(fds[last], src.Get(fds[last]))
	}
}

// clearPath clears the value at fds; an unset intermediate message leaves
// nothing to clear.
func clearPath(m protoreflect.Message, fds []protoreflect.FieldDescriptor) {
	last := len(fds) - 1
	for _, fd := range fds[:last] {
		if !m.Has(fd) {
			return
		}

		m = m.Mutable(fd).Message()
	}

	m.Clear(fds[last])
}
