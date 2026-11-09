// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package validateonly

import (
	"context"
	"errors"
	"fmt"
	"math"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldbehavior"
)

// FieldName is the AIP-163 name of the request field.
const FieldName = "validate_only"

var (
	// ErrInvalidField reports a validate_only field that is not a singular
	// bool.
	ErrInvalidField = errors.New("validateonly: validate_only is not a singular bool")
	// ErrMissingStep reports a nil validate or commit step.
	ErrMissingStep = errors.New("validateonly: missing validate or commit step")
)

// Requested reports the validate_only flag of a request message, found by
// descriptor: false for a nil message or one without the field. A
// validate_only field that is not a singular bool is [ErrInvalidField]
// rather than false, so a malformed request never runs the live write by
// mistake. Generated request types can call GetValidateOnly directly.
func Requested(m proto.Message) (bool, error) {
	if m == nil {
		return false, nil
	}

	r := m.ProtoReflect()

	fd := r.Descriptor().Fields().ByName(FieldName)
	if fd == nil {
		return false, nil
	}

	if fd.Kind() != protoreflect.BoolKind || fd.Cardinality() == protoreflect.Repeated {
		return false, fmt.Errorf("%s.%s: %w", r.Descriptor().FullName(), FieldName, ErrInvalidField)
	}

	return r.Get(fd).Bool(), nil
}

// Run executes a mutation under AIP-163. validate performs every check the
// live request performs, permission checks, field validation and AIP-154
// preconditions included, and returns the response the request would
// produce; commit applies the write and its side effects and returns the
// actual response. When validateOnly is set, Run returns the result of
// validate without calling commit, so a validation request fails exactly
// when the live request would fail before its write, and otherwise returns
// the response the live request would return, minus the values only the
// write produces (see [OmitGenerated]).
//
// For a Create, validate returns the resource as it would be created; for an
// Update, the merged resource; for a Delete, the response the live Delete
// returns (google.protobuf.Empty, or the resource for a soft delete,
// AIP-164). A nil step is [ErrMissingStep], whatever validateOnly says, so a
// misconfigured method fails its validation requests too.
func Run[T any](
	ctx context.Context,
	validateOnly bool,
	validate func(context.Context) (T, error),
	commit func(context.Context, T) (T, error),
) (T, error) {
	var zero T
	if validate == nil || commit == nil {
		return zero, ErrMissingStep
	}

	out, err := validate(ctx)
	if err != nil {
		return zero, err
	}

	if validateOnly {
		return out, nil
	}

	return commit(ctx, out)
}

// OmitGenerated clears the OUTPUT_ONLY fields of a would-be resource at any
// depth: AIP-163 lets a validation response omit the values the server
// generates on the write (a create time, a uid, an etag), which a request
// that writes nothing cannot produce truthfully. A server-generated
// IDENTIFIER (a resource name the service assigns) is the caller's to clear.
func OmitGenerated(m proto.Message) error {
	if m == nil || !m.ProtoReflect().IsValid() {
		return nil
	}

	err := fieldbehavior.Strip(m,
		fieldbehavior.WithBehaviors(annotations.FieldBehavior_OUTPUT_ONLY), fieldbehavior.WithMaxDepth(math.MaxInt))
	if err != nil {
		return fmt.Errorf("validateonly: omit generated values: %w", err)
	}

	return nil
}
