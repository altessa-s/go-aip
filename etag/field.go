// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package etag

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Get returns the value of the etag field of a request resource, or of a
// Delete or custom method request (AIP-154): the singular string field named
// etag, found by descriptor. A message without such a field is
// [ErrNoField]; a nil interface is [ErrNilMessage], while a typed nil
// message reads as "".
func Get(m proto.Message) (string, error) {
	if m == nil {
		return "", ErrNilMessage
	}

	r := m.ProtoReflect()

	fd, err := etagField(r.Descriptor())
	if err != nil {
		return "", err
	}

	return r.Get(fd).String(), nil
}

// Stamp sets the etag field of a response resource to t.String(), so every
// response carries the current etag (AIP-154: the etag is provided by the
// server on output). The zero Tag clears the field. A message without a
// singular string etag field is [ErrNoField]; a nil or typed nil message is
// [ErrNilMessage].
func Stamp(m proto.Message, t Tag) error {
	if m == nil || !m.ProtoReflect().IsValid() {
		return ErrNilMessage
	}

	r := m.ProtoReflect()

	fd, err := etagField(r.Descriptor())
	if err != nil {
		return err
	}

	if t.IsZero() {
		r.Clear(fd)

		return nil
	}

	r.Set(fd, protoreflect.ValueOfString(t.String()))

	return nil
}

// etagField returns the singular string etag field of md.
func etagField(md protoreflect.MessageDescriptor) (protoreflect.FieldDescriptor, error) {
	fd := md.Fields().ByName(FieldName)
	if fd == nil || fd.Kind() != protoreflect.StringKind || fd.Cardinality() == protoreflect.Repeated {
		return nil, fmt.Errorf("etag: %s has no singular string field %q: %w", md.FullName(), FieldName, ErrNoField)
	}

	return fd, nil
}
