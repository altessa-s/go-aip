// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldbehavior

import (
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/internal/behavior"
)

// ValidateRequired reports every REQUIRED field of msg that is absent or set
// to an empty value (AIP-203: a required field must be present and set to a
// non-empty value). Use it on a request message, or on the resource of a
// Create request after [StripCreate]. A field is empty when it is unset (by
// proto presence where the field tracks it), holds the zero value of its
// scalar or enum type (empty string or bytes, 0, false, the zero enum value)
// even with explicit presence, is a repeated or map field without elements,
// or is an unset message; a set message is not empty.
//
// The walk descends into every populated message — singular sub-messages,
// repeated elements ("aliases.2.name") and map values (`labels.k.name`) — and
// never into an absent one: the annotations of a nested message are
// independent of its parent's, so the REQUIRED fields of an optional
// sub-message apply only when it is present. OUTPUT_ONLY and IDENTIFIER
// fields are not input and are skipped. A REQUIRED member of a real oneof is
// satisfied by any member of its oneof, and reported when the oneof holds no
// member or holds this member with an empty value.
//
// Under [WithFieldFormats] (on by default) the same walk reports every value
// of a visited string field that does not match the AIP-202 format of its
// field (google.api.field_info), with Behavior FIELD_BEHAVIOR_UNSPECIFIED: a
// repeated element indexed ("dns_servers.1"), a map value keyed.
//
// It returns *BehaviorViolationError (matching [ErrBehaviorViolation]) with one
// violation per field (per value for a format), in declaration order, or
// [ErrMaxDepthExceeded] past [WithMaxDepth] nested messages. Only
// [WithResolver], [WithMaxDepth], [WithFieldFormats] and [WithFormatChecker]
// apply.
// Update requests check REQUIRED against their field mask instead (see
// [github.com/altessa-s/go-aip/fieldmask/update.Apply]).
func ValidateRequired(msg proto.Message, opts ...Option) error {
	if msg == nil {
		return nil
	}

	o := newOptions(opts...)
	if nilcheck.IsNil(o.resolver) {
		o.resolver = AnnotationResolver{}
	}

	var check behavior.FieldCheck
	if formats := o.formats(); formats != nil {
		check = func(m protoreflect.Message, fd protoreflect.FieldDescriptor, path func() string) []BehaviorViolation {
			if !formats.HasFieldFormat(fd) {
				return nil // no path built for a field without a format
			}

			return appendFormatViolations(nil, formats.CheckField(m, fd, path()))
		}
	}

	violations, exceeded := behavior.CheckedRequiredViolations(msg.ProtoReflect(), "", o.resolveFunc(),
		[]annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY, annotations.FieldBehavior_IDENTIFIER}, o.maxDepth, check)
	if exceeded {
		return ErrMaxDepthExceeded
	}

	if len(violations) > 0 {
		return &BehaviorViolationError{Violations: violations}
	}

	return nil
}
