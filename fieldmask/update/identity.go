// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"slices"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// KeepIdentity reduces msg to the fields that address it under the update
// policy: its set IDENTIFIER fields that are not also OUTPUT_ONLY and the
// top-level identity fields of the policy ([WithIdentityFields]; id and etag
// by default) — the fields [Apply] keeps whatever the mask selects. Every
// other top-level field is cleared, and so is every OUTPUT_ONLY value left
// below a kept field (a message-valued identity field), as Apply clears them.
//
// [Apply] and [ApplyMask] leave the message untouched when the mask ends up
// empty (an empty update_mask, a mask whose every path is dropped): the
// update changes nothing and the message must not be stored. A caller that
// hands the message on anyway, as a gRPC interceptor does, uses KeepIdentity
// to give it the shape any other mask leaves, so values the update does not
// use are neither validated nor seen downstream.
//
// Mutates msg. Pass the same policy used by Apply. A nil msg is left alone.
// The error is the one of the OUTPUT_ONLY walk ([fieldbehavior.Strip]).
func KeepIdentity(msg proto.Message, opts ...Option) error {
	if msg == nil {
		return nil
	}

	prf := msg.ProtoReflect()
	if !prf.IsValid() {
		return nil
	}

	policy := newOptions(opts...)
	keep := identifierFields(prf.Descriptor(), policy)

	fields := prf.Descriptor().Fields()
	for _, name := range policy.identityFields {
		if fd := fields.ByName(protoreflect.Name(name)); fd != nil && isPolicyIdentity(fd, policy) {
			keep = append(keep, fd)
		}
	}

	prf.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if !slices.Contains(keep, fd) {
			prf.Clear(fd)
		}

		return true
	})

	return clearOutputOnly(msg, policy)
}
