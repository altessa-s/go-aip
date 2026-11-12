// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package maskmerge

import (
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// SetValue copies value into fd of dst using destination-created containers.
// A shared descriptor does not imply compatible Go representations, even when
// the roots have the same Go type: dynamic parents can hold generated children.
// Message copies recursively allocate in the destination representation; bytes
// and containers are copied too, so the result never borrows source memory.
// A conversion failure leaves the field untouched and is returned to the caller.
func SetValue(dst protoreflect.Message, fd protoreflect.FieldDescriptor, value protoreflect.Value) error {
	copied := dst.NewField(fd)

	switch {
	case fd.IsList():
		source, target := value.List(), copied.List()
		for i := range source.Len() {
			element, err := copySingular(target.NewElement(), source.Get(i), fd)
			if err != nil {
				return err
			}

			target.Append(element)
		}
	case fd.IsMap():
		target := copied.Map()

		var err error

		value.Map().Range(func(key protoreflect.MapKey, entry protoreflect.Value) bool {
			var element protoreflect.Value

			element, err = copySingular(target.NewValue(), entry, fd.MapValue())
			if err != nil {
				return false
			}

			target.Set(key, element)

			return true
		})

		if err != nil {
			return err
		}
	default:
		var err error

		copied, err = copySingular(copied, value, fd)
		if err != nil {
			return err
		}
	}

	dst.Set(fd, copied)

	return nil
}

// copySingular fills a fresh destination value, including a list or map element.
func copySingular(dst, src protoreflect.Value, fd protoreflect.FieldDescriptor) (protoreflect.Value, error) {
	switch {
	case fd.Message() != nil:
		copied, err := fillMessage(dst.Message(), src.Message())
		if err != nil {
			return protoreflect.Value{}, err
		}

		return protoreflect.ValueOfMessage(copied), nil
	case fd.Kind() == protoreflect.BytesKind:
		return protoreflect.ValueOfBytes(slices.Clone(src.Bytes())), nil
	default:
		return src, nil
	}
}

// convertMessage builds an independent value in dst's descriptor and Go
// representation without modifying dst. Equal names do not permit proto.Merge:
// reconstructed descriptors require wire conversion, including nested values.
func convertMessage(dst, src protoreflect.Message) (protoreflect.Message, error) {
	return fillMessage(dst.New(), src)
}

// fillMessage populates a fresh destination without allocating a second copy.
func fillMessage(out, src protoreflect.Message) (protoreflect.Message, error) {
	if out.Descriptor().FullName() != src.Descriptor().FullName() {
		return nil, fmt.Errorf("fieldmask: cannot convert %s to %s", src.Descriptor().FullName(), out.Descriptor().FullName())
	}

	if out.Descriptor() == src.Descriptor() {
		// Even identical root descriptors can hold children with different
		// identities. Route each child through the same conversion helper.
		var err error

		src.Range(func(fd protoreflect.FieldDescriptor, value protoreflect.Value) bool {
			err = SetValue(out, fd, value)
			return err == nil
		})

		if err != nil {
			return nil, err
		}

		out.SetUnknown(slices.Clone(src.GetUnknown()))

		return out, nil
	}

	wire, err := (proto.MarshalOptions{AllowPartial: true}).Marshal(src.Interface())
	if err != nil {
		return nil, err
	}

	if err := (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(wire, out.Interface()); err != nil {
		return nil, err
	}

	return out, nil
}

// ReplaceMessage replaces dst with an independent copy of src, converting
// descriptor identities and representations before changing dst. Conversion
// failure leaves dst untouched; src may alias dst.
func ReplaceMessage(dst, src protoreflect.Message) error {
	copied, err := convertMessage(dst, src)
	if err != nil {
		return err
	}

	proto.Reset(dst.Interface())
	copied.Range(func(fd protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		dst.Set(fd, value)
		return true
	})
	dst.SetUnknown(copied.GetUnknown())

	return nil
}
