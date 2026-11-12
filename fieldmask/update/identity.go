// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"slices"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldmask"
)

// KeepIdentity reduces msg to the fields that address it under the update
// policy: its set IDENTIFIER fields that are not also OUTPUT_ONLY and the
// top-level identity fields of the policy ([WithIdentityFields]; id and etag
// by default) — the fields [Apply] keeps whatever the mask selects. Every
// other top-level field is cleared, and so are the OUTPUT_ONLY values left
// below a kept field (a message-valued identity field), exactly as Apply
// clears them. Without current state, client OUTPUT_ONLY descendants are always
// cleared, including inside retained protected atomic values. With current state,
// protected descendants of whole values are restored from stored state.
//
// An empty effective mask does not mean validation is complete: protected
// comparisons and covering replacements may still be pending. KeepIdentity
// destroys that input. Interceptors must use [Prepare], attach its retained plan
// with [ContextWithPlan], and leave cleanup until [DeferredPlan.Complete],
// [Spec.Plan] or [Patch.Run] has finished validation and consumed the request
// (including secrets and allow_missing create input). Never store a normalized
// request as the resource. KeepIdentity is optional destructive cleanup, not a
// normalization step or a way to finish protected-field validation.
//
// Mutates msg only on success, preserving its identity. Pass the same policy
// used by Apply. A nil msg is left alone. Errors from cloning or restoring
// values match [ErrMessageMismatch]; sanitization errors are those of the
// OUTPUT_ONLY walk ([fieldbehavior.Strip]). Every error leaves msg unchanged.
func KeepIdentity(msg proto.Message, opts ...Option) error {
	if !validMessage(msg) {
		return nil
	}

	work, err := cloneMessage(msg)
	if err != nil {
		return err
	}

	prf := work.ProtoReflect()
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

	// Apply keeps these fields as whole entries of its mask and clears the
	// OUTPUT_ONLY values below them the same way.
	kept := make(fieldmask.FieldMask, len(keep))
	for _, fd := range keep {
		kept[string(fd.Name())] = nil
	}

	if err := clearOutputOnlyKeeping(work, kept, policy); err != nil {
		return err
	}

	return publishMessage(msg, work)
}
