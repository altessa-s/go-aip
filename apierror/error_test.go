// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package apierror_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/apierror"
)

func TestNewDefaults(t *testing.T) {
	t.Parallel()

	e, err := apierror.New(code.Code_NOT_FOUND, "book not found")
	require.NoError(t, err)
	assert.Equal(t, code.Code_NOT_FOUND, e.Code())
	assert.Equal(t, "book not found", e.Message())
	assert.Equal(t, "NOT_FOUND", e.Reason(), "the reason defaults to the code name")
	assert.Empty(t, e.Domain())
	assert.Nil(t, e.Metadata())
	assert.Nil(t, e.FieldViolations())
	assert.Equal(t, "NOT_FOUND: book not found", e.Error())
	require.NoError(t, e.Unwrap())

	details := e.Details()
	require.Len(t, details, 1, "ErrorInfo is always present")
	assert.IsType(t, &errdetails.ErrorInfo{}, details[0])
}

func TestNewAllDetails(t *testing.T) {
	t.Parallel()

	cause := errors.New("db: row missing")
	extra, err := structpb.NewStruct(map[string]any{"k": "v"})
	require.NoError(t, err)

	e, err := apierror.New(code.Code_FAILED_PRECONDITION, "the shelf is not empty",
		apierror.WithReason("SHELF_NOT_EMPTY"),
		apierror.WithDomain("library.example.com"),
		apierror.WithMetadata(map[string]string{"shelf": "shelves/1", "bookCount": "3"}),
		apierror.WithFieldViolations(&errdetails.BadRequest_FieldViolation{
			Field: "requests[0].shelf.name", Description: "must not be empty", Reason: "EMPTY_NAME",
			LocalizedMessage: &errdetails.LocalizedMessage{Locale: "de-DE", Message: "darf nicht leer sein"},
		}),
		apierror.WithLocalizedMessage("en-US", "Empty the shelf first."),
		apierror.WithHelpLinks(&errdetails.Help_Link{Description: "Shelves", Url: "https://example.com/shelves"}),
		apierror.WithRetryDelay(2*time.Second),
		apierror.WithQuotaViolations(&errdetails.QuotaFailure_Violation{Subject: "project:1", Description: "daily limit"}),
		apierror.WithPreconditionViolations(&errdetails.PreconditionFailure_Violation{Type: "STATE", Subject: "shelves/1"}),
		apierror.WithResourceInfo(&errdetails.ResourceInfo{ResourceType: "library.example.com/Shelf", ResourceName: "shelves/1"}),
		apierror.WithRequestInfo(&errdetails.RequestInfo{RequestId: "req-1"}),
		apierror.WithDebugInfo(&errdetails.DebugInfo{Detail: "stack"}),
		apierror.WithDetails(extra),
		apierror.WithCause(cause),
	)
	require.NoError(t, err)

	assert.Equal(t, "SHELF_NOT_EMPTY", e.Reason())
	assert.Equal(t, "library.example.com", e.Domain())
	assert.Equal(t, map[string]string{"shelf": "shelves/1", "bookCount": "3"}, e.Metadata())
	require.ErrorIs(t, e, cause)
	require.Len(t, e.FieldViolations(), 1)
	assert.Equal(t, "requests[0].shelf.name", e.FieldViolations()[0].GetField())

	types := func(list []proto.Message) []string {
		out := make([]string, len(list))
		for i, m := range list {
			out[i] = string(m.ProtoReflect().Descriptor().Name())
		}

		return out
	}

	assert.Equal(t, []string{
		"ErrorInfo", "BadRequest", "PreconditionFailure", "QuotaFailure", "ResourceInfo", "RetryInfo", "RequestInfo",
		"Help", "LocalizedMessage", "Struct",
	}, types(e.Details()), "DebugInfo is left out by default")
	assert.Contains(t, types(e.Details(apierror.WithIncludeDebugInfo(true))), "DebugInfo")
}

func TestNewCopiesInputs(t *testing.T) {
	t.Parallel()

	md := map[string]string{"shelf": "a"}
	fv := &errdetails.BadRequest_FieldViolation{Field: "name", Description: "bad"}

	e, err := apierror.New(code.Code_INVALID_ARGUMENT, "bad", apierror.WithMetadata(md), apierror.WithFieldViolations(fv))
	require.NoError(t, err)

	md["shelf"] = "changed"
	fv.Field = "changed"

	assert.Equal(t, "a", e.Metadata()["shelf"])
	assert.Equal(t, "name", e.FieldViolations()[0].GetField())

	got := e.Metadata()
	got["shelf"] = "mutated"
	e.FieldViolations()[0].Field = "mutated"
	e.Details()[0].(*errdetails.ErrorInfo).Reason = "MUTATED" //nolint:forcetypeassert,errcheck // test

	assert.Equal(t, "a", e.Metadata()["shelf"])
	assert.Equal(t, "name", e.FieldViolations()[0].GetField())
	assert.Equal(t, "INVALID_ARGUMENT", e.Reason())
}

func TestNewInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code code.Code
		msg  string
		opts []apierror.Option
	}{
		{name: "OK code", code: code.Code_OK, msg: "x"},
		{name: "unknown code", code: code.Code(99), msg: "x"},
		{name: "negative code", code: code.Code(-1), msg: "x"},
		{name: "empty message", code: code.Code_INTERNAL, msg: ""},
		{name: "message not UTF-8", code: code.Code_INTERNAL, msg: "bad \xff"},
		{name: "lower-case reason", code: code.Code_INTERNAL, msg: "x", opts: []apierror.Option{apierror.WithReason("bad_reason")}},
		{name: "reason trailing underscore", code: code.Code_INTERNAL, msg: "x", opts: []apierror.Option{apierror.WithReason("BAD_")}},
		{name: "reason too long", code: code.Code_INTERNAL, msg: "x", opts: []apierror.Option{apierror.WithReason(strings.Repeat("A", 64))}},
		{name: "metadata key upper", code: code.Code_INTERNAL, msg: "x", opts: []apierror.Option{
			apierror.WithMetadata(map[string]string{"Shelf": "a"}),
		}},
		{name: "metadata key too long", code: code.Code_INTERNAL, msg: "x", opts: []apierror.Option{
			apierror.WithMetadata(map[string]string{"k" + strings.Repeat("a", 64): "a"}),
		}},
		{name: "metadata value not UTF-8", code: code.Code_INTERNAL, msg: "x", opts: []apierror.Option{
			apierror.WithMetadata(map[string]string{"shelf": "\xff"}),
		}},
		{name: "violation without field", code: code.Code_INVALID_ARGUMENT, msg: "x", opts: []apierror.Option{
			apierror.WithFieldViolations(&errdetails.BadRequest_FieldViolation{Description: "d"}),
		}},
		{name: "violation nil", code: code.Code_INVALID_ARGUMENT, msg: "x", opts: []apierror.Option{
			apierror.WithFieldViolations(nil),
		}},
		{name: "violation reason", code: code.Code_INVALID_ARGUMENT, msg: "x", opts: []apierror.Option{
			apierror.WithFieldViolations(&errdetails.BadRequest_FieldViolation{Field: "a", Reason: "bad"}),
		}},
		{name: "violation localized without locale", code: code.Code_INVALID_ARGUMENT, msg: "x", opts: []apierror.Option{
			apierror.WithFieldViolations(&errdetails.BadRequest_FieldViolation{
				Field: "a", LocalizedMessage: &errdetails.LocalizedMessage{Message: "m"},
			}),
		}},
		{name: "localized without locale", code: code.Code_INTERNAL, msg: "x", opts: []apierror.Option{
			apierror.WithLocalizedMessage("", "m"),
		}},
		{name: "localized without message", code: code.Code_INTERNAL, msg: "x", opts: []apierror.Option{
			apierror.WithLocalizedMessage("en", ""),
		}},
		{name: "help link without URL", code: code.Code_INTERNAL, msg: "x", opts: []apierror.Option{
			apierror.WithHelpLinks(&errdetails.Help_Link{Description: "d"}),
		}},
		{name: "nil quota violation", code: code.Code_RESOURCE_EXHAUSTED, msg: "x", opts: []apierror.Option{
			apierror.WithQuotaViolations(nil),
		}},
		{name: "nil precondition violation", code: code.Code_FAILED_PRECONDITION, msg: "x", opts: []apierror.Option{
			apierror.WithPreconditionViolations(nil),
		}},
		{name: "nil detail", code: code.Code_INTERNAL, msg: "x", opts: []apierror.Option{apierror.WithDetails((*structpb.Struct)(nil))}},
		{name: "standard detail twice", code: code.Code_NOT_FOUND, msg: "x", opts: []apierror.Option{
			apierror.WithResourceInfo(&errdetails.ResourceInfo{ResourceName: "a"}),
			apierror.WithDetails(&errdetails.ResourceInfo{ResourceName: "b"}),
		}},
		{name: "ErrorInfo as a detail", code: code.Code_NOT_FOUND, msg: "x", opts: []apierror.Option{
			apierror.WithDetails(&errdetails.ErrorInfo{Reason: "OTHER"}),
		}},
		{name: "extra detail twice", code: code.Code_NOT_FOUND, msg: "x", opts: []apierror.Option{
			apierror.WithDetails(&structpb.Struct{}, &structpb.Struct{}),
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := apierror.New(tc.code, tc.msg, tc.opts...)
			require.ErrorIs(t, err, apierror.ErrInvalidError)
		})
	}
}

func TestNewEveryStandardDetailTwice(t *testing.T) {
	t.Parallel()

	for _, d := range []proto.Message{
		&errdetails.BadRequest{}, &errdetails.LocalizedMessage{}, &errdetails.Help{}, &errdetails.RetryInfo{},
		&errdetails.QuotaFailure{}, &errdetails.PreconditionFailure{}, &errdetails.ResourceInfo{}, &errdetails.RequestInfo{},
		&errdetails.DebugInfo{},
	} {
		_, err := apierror.New(code.Code_INTERNAL, "x", apierror.WithDetails(d, proto.Clone(d)))
		require.ErrorIs(t, err, apierror.ErrInvalidError, "%T", d)
	}
}

func TestMustNew(t *testing.T) {
	t.Parallel()

	require.NotPanics(t, func() { apierror.MustNew(code.Code_NOT_FOUND, "x") })
	require.Panics(t, func() { apierror.MustNew(code.Code_OK, "x") })
}

func TestIs(t *testing.T) {
	t.Parallel()

	errNoStock := apierror.MustNew(code.Code_FAILED_PRECONDITION, "out of stock", apierror.WithReason("NO_STOCK"))
	anyPrecondition := apierror.MustNew(code.Code_FAILED_PRECONDITION, "x")

	got := apierror.MustNew(code.Code_FAILED_PRECONDITION, "item 7 is out of stock", apierror.WithReason("NO_STOCK"))
	wrapped := fmt.Errorf("order: %w", got)

	require.ErrorIs(t, wrapped, errNoStock)
	require.NotErrorIs(t, wrapped, anyPrecondition, "a reason-less target still compares the derived reason")
	require.NotErrorIs(t, wrapped, apierror.MustNew(code.Code_NOT_FOUND, "x", apierror.WithReason("NO_STOCK")))
	require.NotErrorIs(t, wrapped, apierror.MustNew(code.Code_FAILED_PRECONDITION, "x", apierror.WithReason("OTHER")))
	require.NotErrorIs(t, wrapped, errors.New("other"))
	require.NotErrorIs(t, wrapped, (*apierror.Error)(nil))

	as, ok := errors.AsType[*apierror.Error](wrapped)
	require.True(t, ok)
	require.Same(t, got, as)
}

func TestValidReasonAndKey(t *testing.T) {
	t.Parallel()

	for _, r := range []string{"NOT_FOUND", "A1B", "CPU_AVAILABILITY", strings.Repeat("A", 63)} {
		assert.True(t, apierror.ValidReason(r), r)
	}

	for _, r := range []string{"", "AB", "A_", "1AB", "_AB", "Ab_C", "A-B", strings.Repeat("A", 64)} {
		assert.False(t, apierror.ValidReason(r), r)
	}

	for _, k := range []string{"shelf", "bookCount", "a1", "x-y", "x_y", "k" + strings.Repeat("a", 63)} {
		assert.True(t, apierror.ValidMetadataKey(k), k)
	}

	for _, k := range []string{"", "a", "Shelf", "1a", "a.b", "a b", "k" + strings.Repeat("a", 64)} {
		assert.False(t, apierror.ValidMetadataKey(k), k)
	}
}
