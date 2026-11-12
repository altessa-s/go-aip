// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package behavior

import (
	"bytes"
	"slices"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// SameInputValue compares a protected field as SameValue does, excluding
// OUTPUT_ONLY descendants under resolve. Those descendants are ignored input,
// so stripping them before comparison cannot turn an unchanged value into a
// change. Presence of the protected field itself still participates. Comparison
// reads reflection values directly and never converts or mutates either input.
func SameInputValue(r, c protoreflect.Message, fd protoreflect.FieldDescriptor, resolve Resolve) bool {
	if SameValue(r, c, fd) {
		return true
	}

	if fd.Message() == nil || r == nil || c == nil || !r.IsValid() || !c.IsValid() || r.Has(fd) != c.Has(fd) {
		return false
	}

	return sameInputField(r.Get(fd), c.Get(fd), fd, resolve)
}

func sameInputField(r, c protoreflect.Value, fd protoreflect.FieldDescriptor, resolve Resolve) bool {
	switch {
	case fd.IsMap() && fd.MapValue().Message() != nil:
		rm, cm := r.Map(), c.Map()
		if rm.Len() != cm.Len() {
			return false
		}

		equal := true

		rm.Range(func(key protoreflect.MapKey, value protoreflect.Value) bool {
			equal = cm.Has(key) && sameInputMessage(value.Message(), cm.Get(key).Message(), resolve)
			return equal
		})

		return equal
	case fd.IsList() && fd.Message() != nil:
		rl, cl := r.List(), c.List()
		if rl.Len() != cl.Len() {
			return false
		}

		for i := range rl.Len() {
			if !sameInputMessage(rl.Get(i).Message(), cl.Get(i).Message(), resolve) {
				return false
			}
		}

		return true
	case !fd.IsList() && !fd.IsMap() && fd.Message() != nil:
		return sameInputMessage(r.Message(), c.Message(), resolve)
	default:
		return r.Equal(c)
	}
}

func sameInputMessage(r, c protoreflect.Message, resolve Resolve) bool {
	if r.Descriptor() != c.Descriptor() {
		return false
	}

	equal := true
	rCount, cCount := 0, 0

	r.Range(func(fd protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if slices.Contains(resolve(fd), annotations.FieldBehavior_OUTPUT_ONLY) {
			return true
		}

		rCount++
		equal = c.Has(fd) && sameInputField(value, c.Get(fd), fd, resolve)

		return equal
	})

	if !equal {
		return false
	}

	c.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if !slices.Contains(resolve(fd), annotations.FieldBehavior_OUTPUT_ONLY) {
			cCount++
		}

		return true
	})

	if rCount != cCount {
		return false
	}

	if bytes.Equal(r.GetUnknown(), c.GetUnknown()) {
		return true
	}

	// Delegate unknown-field ordering to protobuf equality, without copying
	// any populated values or converting their representations.
	ru, cu := r.New(), c.New()
	ru.SetUnknown(r.GetUnknown())
	cu.SetUnknown(c.GetUnknown())

	return protoreflect.ValueOfMessage(ru).Equal(protoreflect.ValueOfMessage(cu))
}
