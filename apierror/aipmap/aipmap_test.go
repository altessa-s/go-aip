// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package aipmap_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/apierror"
	"github.com/altessa-s/go-aip/apierror/aipmap"
	"github.com/altessa-s/go-aip/batch"
	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldinfo"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/projection"
	"github.com/altessa-s/go-aip/projection/protopath"
	"github.com/altessa-s/go-aip/requestid"
	"github.com/altessa-s/go-aip/resourcename"
	"github.com/altessa-s/go-aip/softdelete"
	"github.com/altessa-s/go-aip/state"
	"github.com/altessa-s/go-aip/unreachable"

	testpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func newMapper(t *testing.T) *apierror.Mapper {
	t.Helper()

	m, err := apierror.NewMapper(apierror.WithRules(aipmap.Rules()...), apierror.WithDefaultDomain("example.com"))
	require.NoError(t, err, "every rule is valid")

	return m
}

func TestRules(t *testing.T) {
	t.Parallel()

	const (
		invalid    = code.Code_INVALID_ARGUMENT
		internal   = code.Code_INTERNAL
		exhausted  = code.Code_RESOURCE_EXHAUSTED
		failedPrec = code.Code_FAILED_PRECONDITION
	)

	tests := []struct {
		err    error
		code   code.Code
		reason string
	}{
		{listing.ErrInvalidSchema, internal, apierror.ReasonInternal},
		{filter.ErrPolicyRequired, internal, apierror.ReasonInternal},
		{orderby.ErrAllowlistRequired, internal, apierror.ReasonInternal},
		{projection.ErrConflictingPolicy, internal, apierror.ReasonInternal},
		{protopath.ErrDescriptorRequired, internal, apierror.ReasonInternal},
		{update.ErrInvalidPatch, internal, apierror.ReasonInternal},
		{fieldbehavior.ErrCurrentMismatch, internal, apierror.ReasonInternal},
		{pagetoken.ErrWeakKey, internal, apierror.ReasonInternal},
		{update.ErrBehaviorViolation, invalid, aipmap.ReasonFieldBehaviorViolation},
		{fieldbehavior.ErrBehaviorViolation, invalid, aipmap.ReasonFieldBehaviorViolation},
		{update.ErrProtectedField, invalid, aipmap.ReasonProtectedField},
		{fieldmask.ErrMaxPathsExceeded, invalid, aipmap.ReasonFieldMaskTooComplex},
		{fieldmask.ErrInvalidPath, invalid, aipmap.ReasonInvalidFieldMask},
		{fieldmask.ErrWildcardNotAlone, invalid, aipmap.ReasonInvalidFieldMask},
		{update.ErrElementCountMismatch, failedPrec, aipmap.ReasonElementCountMismatch},
		{filter.ErrFieldNotAllowed, invalid, aipmap.ReasonFilterNotAllowed},
		{filter.ErrMaxOperationsExceeded, invalid, aipmap.ReasonFilterTooComplex},
		{filter.ErrParseFailed, invalid, aipmap.ReasonInvalidFilter},
		{filter.ErrInvalidRegex, invalid, aipmap.ReasonInvalidFilter},
		{filter.ErrDataLimitExceeded, exhausted, aipmap.ReasonWorkBudgetExceeded},
		{orderby.ErrFieldNotAllowed, invalid, aipmap.ReasonOrderByNotAllowed},
		{listing.ErrUnreadableSortField, invalid, aipmap.ReasonOrderByNotAllowed},
		{orderby.ErrMaxKeysExceeded, invalid, aipmap.ReasonOrderByTooComplex},
		{orderby.ErrInvalidDirection, invalid, aipmap.ReasonInvalidOrderBy},
		{projection.ErrFieldNotAllowed, invalid, aipmap.ReasonReadMaskNotAllowed},
		{projection.ErrMaxPathsExceeded, invalid, aipmap.ReasonReadMaskTooComplex},
		{projection.ErrInvalidView, invalid, aipmap.ReasonInvalidReadMask},
		{protopath.ErrUnknownField, invalid, aipmap.ReasonInvalidReadMask},
		{pagetoken.ErrExpiredToken, invalid, aipmap.ReasonExpiredPageToken},
		{pagetoken.ErrUnencryptedToken, invalid, aipmap.ReasonInvalidPageToken},
		{pagetoken.ErrSubjectMismatch, invalid, aipmap.ReasonInvalidPageToken},
		{batch.ErrNoTransaction, internal, apierror.ReasonInternal},
		{batch.ErrTooManyItems, invalid, aipmap.ReasonBatchTooLarge},
		{batch.ErrEmpty, invalid, aipmap.ReasonInvalidBatch},
		{batch.ErrParentMismatch, invalid, aipmap.ReasonInvalidBatch},
		{batch.ErrAllFailed, code.Code_ABORTED, aipmap.ReasonAllRequestsFailed},
		{batch.ErrNotFound, code.Code_NOT_FOUND, apierror.ReasonNotFound},
		{softdelete.ErrFilterTooLong, invalid, aipmap.ReasonPurgeTooLarge},
		{softdelete.ErrTooManyResources, invalid, aipmap.ReasonPurgeTooLarge},
		{softdelete.ErrUnsupportedResource, internal, apierror.ReasonInternal},
		{softdelete.ErrAlreadyDeleted, code.Code_NOT_FOUND, apierror.ReasonNotFound},
		{softdelete.ErrNotDeleted, code.Code_ALREADY_EXISTS, apierror.ReasonAlreadyExists},
		{requestid.ErrInvalidRequestID, invalid, aipmap.ReasonInvalidRequestID},
		{requestid.ErrRequestIDReused, code.Code_ALREADY_EXISTS, aipmap.ReasonRequestIDReused},
		{requestid.ErrInProgress, code.Code_ABORTED, aipmap.ReasonRequestInProgress},
		{requestid.ErrResponseNotRetained, failedPrec, aipmap.ReasonResponseNotRetained},
		{requestid.ErrStore, code.Code_UNAVAILABLE, apierror.ReasonUnavailable},
		{requestid.ErrNoRequestIDField, internal, apierror.ReasonInternal},
		{requestid.ErrReplayedFailure, internal, apierror.ReasonInternal},
		{unreachable.ErrUnreachable, code.Code_UNAVAILABLE, aipmap.ReasonResourcesUnreachable},
		{unreachable.ErrPartialSuccessUnsupported, invalid, aipmap.ReasonPartialSuccessUnsupported},
		{unreachable.ErrInvalidSource, internal, apierror.ReasonInternal},
		{state.ErrTransitionNotAllowed, failedPrec, aipmap.ReasonTransitionNotAllowed},
		{state.ErrInvalidTable, internal, apierror.ReasonInternal},
		{resourcename.ErrNoMatch, invalid, aipmap.ReasonInvalidResourceName},
		{resourcename.ErrLimitExceeded, invalid, aipmap.ReasonInvalidResourceName},
		{resourcename.ErrInvalidUID, invalid, aipmap.ReasonInvalidUID},
		{resourcename.ErrUnknownType, internal, apierror.ReasonInternal},
	}

	m := newMapper(t)
	for _, tc := range tests {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()

			e := m.Map(fmt.Errorf("op: %w", tc.err))
			assert.Equal(t, tc.code, e.Code())
			assert.Equal(t, tc.reason, e.Reason())
			assert.Equal(t, "example.com", e.Domain())

			switch {
			case tc.code == code.Code_INVALID_ARGUMENT || tc.err == state.ErrTransitionNotAllowed:
				assert.Equal(t, "op: "+tc.err.Error(), e.Message(), "client input errors expose their text")
			case tc.code == code.Code_INTERNAL:
				assert.Equal(t, apierror.MessageInternal, e.Message())
			default:
				assert.NotContains(t, e.Message(), "op:")
			}
		})
	}
}

func TestFieldErrorWrappingASentinel(t *testing.T) {
	t.Parallel()

	m := newMapper(t)

	e := m.Map(&coreerrs.FieldError{Field: "filter", Reason: "too many operations", Err: filter.ErrMaxOperationsExceeded})
	assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
	assert.Equal(t, aipmap.ReasonFilterTooComplex, e.Reason())
	assert.Equal(t, "filter: too many operations", e.Message())
	require.Len(t, e.FieldViolations(), 1)
	assert.Equal(t, "filter", e.FieldViolations()[0].GetField())
	assert.Equal(t, aipmap.ReasonFilterTooComplex, e.FieldViolations()[0].GetReason())

	e = m.Map(&coreerrs.FieldError{Field: "pageSize", Reason: "schema bug", Err: listing.ErrInvalidSchema})
	assert.Equal(t, code.Code_INTERNAL, e.Code(), "a configuration fault is not blamed on the request")
	assert.Equal(t, apierror.MessageInternal, e.Message())
	assert.Empty(t, e.FieldViolations())

	e = m.Map(coreerrs.NewFieldError("pageSize", "must not be negative"))
	assert.Equal(t, apierror.ReasonInvalidArgument, e.Reason(), "the default rules still apply")
}

func TestFieldMaskPlanErrors(t *testing.T) {
	t.Parallel()

	spec, err := update.NewSpec(&testpb.UpdateRequest{}, update.WithMaxPaths(2))
	require.NoError(t, err)

	m := newMapper(t)

	_, err = spec.Plan(&testpb.UpdateRequest{}, &fieldmaskpb.FieldMask{Paths: []string{"description", "no_such_field"}})
	require.Error(t, err)

	e := m.Map(err)
	assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
	assert.Equal(t, aipmap.ReasonInvalidFieldMask, e.Reason())
	require.Len(t, e.FieldViolations(), 1)
	assert.Equal(t, "updateMask.paths[1]", e.FieldViolations()[0].GetField())

	_, err = spec.Plan(&testpb.UpdateRequest{}, &fieldmaskpb.FieldMask{Paths: []string{"a", "b", "c"}})
	require.Error(t, err)

	e = m.Map(err)
	assert.Equal(t, aipmap.ReasonFieldMaskTooComplex, e.Reason(), "a limit error wins over the invalid path it also matches")
	assert.Equal(t, "updateMask", e.FieldViolations()[0].GetField())
}

func TestOrderByParseError(t *testing.T) {
	t.Parallel()

	p, err := orderby.NewParser()
	require.NoError(t, err)

	_, err = p.Parse(t.Context(), "title sideways")
	require.Error(t, err)

	e := newMapper(t).Map(err)
	assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
	assert.Equal(t, aipmap.ReasonInvalidOrderBy, e.Reason())
	assert.Equal(t, err.Error(), e.Message())
}

func TestRulesAreFresh(t *testing.T) {
	t.Parallel()

	a := aipmap.Rules()
	a[0].Target = errors.New("x")
	assert.NotEqual(t, a[0].Target, aipmap.Rules()[0].Target)
}

func TestReplayedFailure(t *testing.T) {
	t.Parallel()

	m := newMapper(t)

	info, err := anypb.New(&errdetails.ErrorInfo{Reason: "NO_STOCK", Domain: "shop.example.com"})
	require.NoError(t, err)

	stored := &status.Status{Code: int32(code.Code_FAILED_PRECONDITION), Message: "out of stock", Details: []*anypb.Any{info}}
	e := m.Map(fmt.Errorf("create: %w", &requestid.ReplayedError{Status: stored}))
	assert.Equal(t, code.Code_FAILED_PRECONDITION, e.Code(), "the stored status is replayed")
	assert.Equal(t, "NO_STOCK", e.Reason())
	assert.Equal(t, "shop.example.com", e.Domain(), "its domain is kept")

	e = m.Map(&requestid.ReplayedError{Status: &status.Status{Code: int32(code.Code_ABORTED), Message: "busy"}})
	assert.Equal(t, code.Code_ABORTED, e.Code(), "a stored status without ErrorInfo keeps code and message")
	assert.Equal(t, "busy", e.Message())
	assert.Equal(t, "ABORTED", e.Reason())
	assert.Equal(t, "example.com", e.Domain())

	e = m.Map(&requestid.ReplayedError{Status: &status.Status{Code: 0}})
	assert.Equal(t, code.Code_INTERNAL, e.Code(), "a stored OK status falls back to the rule")
	assert.Equal(t, apierror.MessageInternal, e.Message())

	e = m.Map(&requestid.ReplayedError{})
	assert.Equal(t, code.Code_INTERNAL, e.Code())
}

func TestStateTransitionExposed(t *testing.T) {
	t.Parallel()

	e := newMapper(t).Map(fmt.Errorf("%w: ACTIVE to DELETED", state.ErrTransitionNotAllowed))
	assert.Equal(t, code.Code_FAILED_PRECONDITION, e.Code())
	assert.Contains(t, e.Message(), "ACTIVE to DELETED")
}

// TestClassifiedCauseStaysOutOfConverters checks the violation converter of a visible protected-field error ignores the stored
// record violations a classified decoder failure joined with it hides: its reason, violations and message are the visible error's.
func TestClassifiedCauseStaysOutOfConverters(t *testing.T) {
	t.Parallel()

	errCorrupt := errors.New("stored record is corrupt")
	stored := &fieldinfo.ViolationError{Violations: []fieldinfo.Violation{{Path: "secretPath", Format: "UUID", Reason: "secret stored value"}}}
	protected := fmt.Errorf("%w: path \"name\" cannot be changed", update.ErrProtectedField)

	e := newMapper(t).Map(errors.Join(apierror.Classify(errCorrupt, stored), protected))
	assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
	assert.Equal(t, aipmap.ReasonProtectedField, e.Reason())
	assert.Empty(t, e.FieldViolations())
	assert.NotContains(t, e.Message(), "secret")
	assert.Contains(t, e.Message(), `path "name" cannot be changed`)
	assert.Same(t, stored, apierror.HiddenCause(e), "the mapped error keeps the original cause")
}

// TestClassifiedCauseBelowAFieldError checks a FieldError whose reason formats a classified error is reported with the category
// alone: the violations a classified error hides reach neither its reason nor the violations aipmap builds.
func TestClassifiedCauseBelowAFieldError(t *testing.T) {
	t.Parallel()

	const secret = "secret stored value"

	stored := &fieldinfo.ViolationError{Violations: []fieldinfo.Violation{{Path: "secretPath", Format: "UUID", Reason: secret}}}
	c := apierror.Classify(errors.New("stored record is corrupt"), stored)

	e := newMapper(t).Map(&coreerrs.FieldError{Field: "item", Reason: c.Error(), Err: c})
	assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
	require.Len(t, e.FieldViolations(), 1)
	assert.Equal(t, "item", e.FieldViolations()[0].GetField())
	assert.Equal(t, "stored record is corrupt", e.FieldViolations()[0].GetDescription())

	for _, text := range []string{e.Message(), e.Error(), prototext.Format(e.Proto())} {
		assert.NotContains(t, text, secret)
		assert.NotContains(t, text, "secretPath")
	}
}
