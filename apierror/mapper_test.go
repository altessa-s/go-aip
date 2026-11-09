// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package apierror_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/rpc/code"

	"github.com/altessa-s/go-aip/apierror"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestFromErrorDefaultRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		code   code.Code
		reason string
		msg    string
	}{
		{"not found", fmt.Errorf("book 7: %w", coreerrs.ErrNotFound), code.Code_NOT_FOUND, "NOT_FOUND", apierror.MessageNotFound},
		{"already exists", coreerrs.ErrAlreadyExists, code.Code_ALREADY_EXISTS, "ALREADY_EXISTS", apierror.MessageAlreadyExists},
		{"conflict", fmt.Errorf("save: %w", coreerrs.ErrConflict), code.Code_ABORTED, "VERSION_CONFLICT", apierror.MessageConflict},
		{
			"invalid argument exposes its text", fmt.Errorf("%w: bad cursor", coreerrs.ErrInvalidArgument),
			code.Code_INVALID_ARGUMENT, "INVALID_ARGUMENT", "invalid argument: bad cursor",
		},
		{
			"failed precondition", coreerrs.ErrFailedPrecondition, code.Code_FAILED_PRECONDITION, "FAILED_PRECONDITION",
			apierror.MessageFailedPrecondition,
		},
		{
			"unavailable hides its text", fmt.Errorf("mongo 10.0.0.1: %w", coreerrs.ErrUnavailable), code.Code_UNAVAILABLE,
			"TEMPORARY_UNAVAILABLE", apierror.MessageUnavailable,
		},
		{"busy", coreerrs.ErrBusy, code.Code_ABORTED, "BUSY", apierror.MessageBusy},
		{"canceled", fmt.Errorf("q: %w", context.Canceled), code.Code_CANCELLED, apierror.ReasonCanceled, apierror.MessageCanceled}, //nolint:misspell // google.rpc.Code
		{
			"deadline", context.DeadlineExceeded, code.Code_DEADLINE_EXCEEDED, "DEADLINE_EXCEEDED",
			apierror.MessageDeadlineExceeded,
		},
		{
			"unknown is INTERNAL without its text", errors.New("dial tcp 10.0.0.1: refused"), code.Code_INTERNAL,
			"INTERNAL", apierror.MessageInternal,
		},
		{
			"not found wins over a joined field error",
			errors.Join(coreerrs.NewFieldError("name", "bad"), coreerrs.ErrNotFound), code.Code_NOT_FOUND, "NOT_FOUND",
			apierror.MessageNotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := apierror.FromError(tc.err)
			require.NotNil(t, e)
			assert.Equal(t, tc.code, e.Code())
			assert.Equal(t, tc.reason, e.Reason())
			assert.Equal(t, tc.msg, e.Message())
			assert.Empty(t, e.FieldViolations())
			require.ErrorIs(t, e, tc.err, "the cause stays in the chain")
		})
	}
}

func TestFromErrorNil(t *testing.T) {
	t.Parallel()

	require.Nil(t, apierror.FromError(nil))
}

func TestFromErrorFieldError(t *testing.T) {
	t.Parallel()

	cause := errors.New("etag: malformed")
	err := fmt.Errorf("update: %w", &coreerrs.FieldError{Field: "updateMask.paths[2]", Reason: "unknown field", Err: cause})

	e := apierror.FromError(err)
	assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
	assert.Equal(t, "INVALID_ARGUMENT", e.Reason())
	assert.Equal(t, "updateMask.paths[2]: unknown field", e.Message())
	require.Len(t, e.FieldViolations(), 1)
	v := e.FieldViolations()[0]
	assert.Equal(t, "updateMask.paths[2]", v.GetField(), "the path is kept as produced")
	assert.Equal(t, "unknown field", v.GetDescription())
	assert.Equal(t, "INVALID_ARGUMENT", v.GetReason())
	require.ErrorIs(t, e, cause)

	mapped := coreerrs.MapFieldPath(coreerrs.NewFieldError("title", "required"), func(f string) string { return "requests[0]." + f })
	assert.Equal(t, "requests[0].title", apierror.FromError(mapped).FieldViolations()[0].GetField())
}

func TestFromErrorFieldErrorNotUTF8(t *testing.T) {
	t.Parallel()

	e := apierror.FromError(coreerrs.NewFieldError("name", "bad \xff"))
	assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
	assert.Equal(t, "invalid argument", e.Message(), "falls back to the fixed message")
	assert.Empty(t, e.FieldViolations())
}

func TestFromErrorKeepsAPIError(t *testing.T) {
	t.Parallel()

	apiErr := apierror.MustNew(code.Code_PERMISSION_DENIED, "permission denied on shelves/1")
	require.Same(t, apiErr, apierror.FromError(apiErr), "an *Error maps to itself")

	wrapped := fmt.Errorf("get: %w", apiErr)
	mapped := apierror.FromError(wrapped)
	assert.Equal(t, apiErr.Code(), mapped.Code())
	assert.Equal(t, apiErr.Message(), mapped.Message())
	assert.Same(t, wrapped, errors.Unwrap(mapped), "the original error is the cause")
	require.ErrorIs(t, mapped, apiErr)
}

func TestMapperRulesAndDomain(t *testing.T) {
	t.Parallel()

	errQuota := errors.New("quota exhausted for tenant 42")
	errInput := errors.New("bad shelf name")
	errConfig := errors.New("missing policy")

	m, err := apierror.NewMapper(
		apierror.WithRules(
			apierror.Rule{Target: errQuota, Code: code.Code_RESOURCE_EXHAUSTED, Reason: "TENANT_QUOTA"},
			apierror.Rule{Target: errInput, Code: code.Code_INVALID_ARGUMENT, Reason: "INVALID_SHELF", Expose: true},
			apierror.Rule{Target: errConfig, Code: code.Code_INTERNAL, Expose: true},
			apierror.Rule{Target: coreerrs.ErrNotFound, Code: code.Code_PERMISSION_DENIED, Message: "permission denied"},
		),
		apierror.WithDefaultDomain("library.example.com"),
	)
	require.NoError(t, err)

	e := m.Map(fmt.Errorf("charge: %w", errQuota))
	assert.Equal(t, code.Code_RESOURCE_EXHAUSTED, e.Code())
	assert.Equal(t, "TENANT_QUOTA", e.Reason())
	assert.Equal(t, "resource exhausted", e.Message(), "a fixed message by default")
	assert.Equal(t, "library.example.com", e.Domain())

	e = m.Map(fmt.Errorf("create: %w", errInput))
	assert.Equal(t, "create: bad shelf name", e.Message())

	e = m.Map(errConfig)
	assert.Equal(t, apierror.MessageInternal, e.Message(), "INTERNAL never exposes")

	e = m.Map(coreerrs.ErrNotFound)
	assert.Equal(t, code.Code_PERMISSION_DENIED, e.Code(), "service rules win over the defaults")
	assert.Equal(t, "PERMISSION_DENIED", e.Reason())

	e = m.Map(&coreerrs.FieldError{Field: "shelf.name", Reason: "bad", Err: errInput})
	assert.Equal(t, "INVALID_SHELF", e.Reason())
	assert.Equal(t, "shelf.name: bad", e.Message())
	assert.Equal(t, "INVALID_SHELF", e.FieldViolations()[0].GetReason())

	e = m.Map(coreerrs.ErrConflict)
	assert.Equal(t, code.Code_ABORTED, e.Code(), "defaults still apply")

	own := apierror.MustNew(code.Code_NOT_FOUND, "x")
	e = m.Map(own)
	assert.Equal(t, "library.example.com", e.Domain(), "the domain is filled in")
	assert.Empty(t, own.Domain(), "on a copy")

	withDomain := apierror.MustNew(code.Code_NOT_FOUND, "x", apierror.WithDomain("other.example.com"))
	require.Same(t, withDomain, m.Map(withDomain))
}

func TestMapperExposeUnknown(t *testing.T) {
	t.Parallel()

	m, err := apierror.NewMapper(apierror.WithExposeUnknown(true))
	require.NoError(t, err)
	assert.Equal(t, "boom", m.Map(errors.New("boom")).Message())
	assert.Equal(t, apierror.MessageInternal, m.Map(errors.New("bad \xff")).Message())
	assert.Equal(t, apierror.MessageInternal, m.Map(errors.New("  ")).Message())
}

func TestNewMapperInvalid(t *testing.T) {
	t.Parallel()

	target := errors.New("x")
	for name, opt := range map[string]apierror.MapperOption{
		"no target":      apierror.WithRules(apierror.Rule{Code: code.Code_INTERNAL}),
		"OK code":        apierror.WithRules(apierror.Rule{Target: target}),
		"bad reason":     apierror.WithRules(apierror.Rule{Target: target, Code: code.Code_INTERNAL, Reason: "bad"}),
		"message":        apierror.WithRules(apierror.Rule{Target: target, Code: code.Code_INTERNAL, Message: "\xff"}),
		"domain":         apierror.WithDefaultDomain("\xff"),
		"code too large": apierror.WithRules(apierror.Rule{Target: target, Code: code.Code(17)}),
	} {
		_, err := apierror.NewMapper(opt)
		require.ErrorIs(t, err, apierror.ErrInvalidError, name)
	}
}

func TestDefaultRulesAreFresh(t *testing.T) {
	t.Parallel()

	a := apierror.DefaultRules()
	a[0].Code = code.Code_INTERNAL
	assert.Equal(t, code.Code_NOT_FOUND, apierror.DefaultRules()[0].Code)
	assert.Equal(t, code.Code_NOT_FOUND, apierror.FromError(coreerrs.ErrNotFound).Code())
}

func TestRuleConvert(t *testing.T) {
	t.Parallel()

	errA, errB := errors.New("a"), errors.New("b")
	built := apierror.MustNew(code.Code_OUT_OF_RANGE, "past the end")

	m, err := apierror.NewMapper(
		apierror.WithRules(
			apierror.Rule{Target: errA, Code: code.Code_INTERNAL, Convert: func(error) *apierror.Error { return built }},
			apierror.Rule{Target: errB, Code: code.Code_UNAVAILABLE, Convert: func(error) *apierror.Error { return nil }},
		),
		apierror.WithDefaultDomain("example.com"))
	require.NoError(t, err)

	e := m.Map(errA)
	assert.Equal(t, code.Code_OUT_OF_RANGE, e.Code())
	assert.Equal(t, "example.com", e.Domain())

	assert.Equal(t, code.Code_UNAVAILABLE, m.Map(errB).Code(), "a nil conversion falls back to the rule")
}
