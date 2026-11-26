// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package aipmap_test

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/genproto/googleapis/rpc/status"

	"github.com/altessa-s/go-aip/apierror"
	"github.com/altessa-s/go-aip/apierror/aipmap"
	"github.com/altessa-s/go-aip/batch"
	"github.com/altessa-s/go-aip/etag"
	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldinfo"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/extract"
	"github.com/altessa-s/go-aip/fieldmask/update"
	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/regexanchor"
	"github.com/altessa-s/go-aip/internal/testhelpers"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/evaluation"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/projection"
	"github.com/altessa-s/go-aip/projection/protopath"
	"github.com/altessa-s/go-aip/requestid"
	"github.com/altessa-s/go-aip/resourcename"
	"github.com/altessa-s/go-aip/softdelete"
	"github.com/altessa-s/go-aip/state"
	"github.com/altessa-s/go-aip/unreachable"
	"github.com/altessa-s/go-aip/validateonly"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// expectation is the code and reason an error maps to.
type expectation struct {
	err    error
	code   code.Code
	reason string
}

const (
	cInvalid  = code.Code_INVALID_ARGUMENT
	cInternal = code.Code_INTERNAL
	rInternal = apierror.ReasonInternal
)

// sentinels lists every exported Err* variable of the module outside backend
// and internal packages, keyed "<package dir>.<name>", with the error it is
// mapped as (the sentinel itself, or wrapped the way its package returns it)
// and the intended code and reason. TestEverySentinelIsListed fails for a
// sentinel missing here, so a new one gets a rule or a deliberate default.
var sentinels = map[string]expectation{
	// Not a go-aip input error: an invalid *apierror.Error is a server bug.
	"apierror.ErrInvalidError": {apierror.ErrInvalidError, cInternal, rInternal},

	"batch.ErrAllFailed":       {batch.ErrAllFailed, code.Code_ABORTED, aipmap.ReasonAllRequestsFailed},
	"batch.ErrDuplicate":       {batch.ErrDuplicate, cInvalid, aipmap.ReasonInvalidBatch},
	"batch.ErrEmpty":           {batch.ErrEmpty, cInvalid, aipmap.ReasonInvalidBatch},
	"batch.ErrHoistedMismatch": {batch.ErrHoistedMismatch, cInvalid, aipmap.ReasonInvalidBatch},
	"batch.ErrNoTransaction":   {batch.ErrNoTransaction, cInternal, rInternal},
	"batch.ErrNotBatchRequest": {batch.ErrNotBatchRequest, cInternal, rInternal},
	"batch.ErrNotFound":        {batch.ErrNotFound, code.Code_NOT_FOUND, apierror.ReasonNotFound}, // default rule
	"batch.ErrParentMismatch":  {batch.ErrParentMismatch, cInvalid, aipmap.ReasonInvalidBatch},
	"batch.ErrTooManyItems":    {batch.ErrTooManyItems, cInvalid, aipmap.ReasonBatchTooLarge},
	"etag.ErrInvalid":          {etag.ErrInvalid, cInvalid, aipmap.ReasonInvalidEtag},
	"etag.ErrMismatch":         {etag.ErrMismatch, code.Code_ABORTED, apierror.ReasonVersionConflict}, // ErrConflict
	"etag.ErrMissing":          {etag.ErrMissing, cInvalid, aipmap.ReasonEtagRequired},
	"etag.ErrNilMessage":       {etag.ErrNilMessage, cInternal, rInternal},
	"etag.ErrNoField":          {etag.ErrNoField, cInternal, rInternal},
	"etag.ErrNotRevision":      {etag.ErrNotRevision, cInvalid, aipmap.ReasonInvalidEtag},
	"fieldbehavior.ErrBehaviorViolation": {
		fieldbehavior.ErrBehaviorViolation, cInvalid, aipmap.ReasonFieldBehaviorViolation,
	},
	"fieldbehavior.ErrCurrentMismatch":      {fieldbehavior.ErrCurrentMismatch, cInternal, rInternal},
	"fieldbehavior.ErrMaxDepthExceeded":     {fieldbehavior.ErrMaxDepthExceeded, cInvalid, aipmap.ReasonMessageTooDeep},
	"fieldinfo.ErrInvalidRegistry":          {fieldinfo.ErrInvalidRegistry, cInternal, rInternal},
	"fieldinfo.ErrInvalidValue":             {fieldinfo.ErrInvalidValue, cInvalid, aipmap.ReasonInvalidFieldFormat},
	"fieldinfo.ErrMaxDepthExceeded":         {fieldinfo.ErrMaxDepthExceeded, cInvalid, aipmap.ReasonMessageTooDeep},
	"fieldinfo.ErrUnknownFormat":            {fieldinfo.ErrUnknownFormat, cInternal, rInternal},
	"fieldmask/update.ErrBehaviorViolation": {update.ErrBehaviorViolation, cInvalid, aipmap.ReasonFieldBehaviorViolation},
	"fieldmask/update.ErrElementCountMismatch": {
		update.ErrElementCountMismatch, code.Code_FAILED_PRECONDITION, aipmap.ReasonElementCountMismatch,
	},
	"fieldmask/update.ErrAllowedPathsAtCall": {
		update.ErrAllowedPathsAtCall, cInternal, rInternal,
	},
	"fieldmask/extract.ErrFieldNotSettable": {extract.ErrFieldNotSettable, cInternal, rInternal},
	"fieldmask/update.ErrInvalidPatch":      {update.ErrInvalidPatch, cInternal, rInternal},
	"fieldmask.ErrInvalidPath":              {fieldmask.ErrInvalidPath, cInvalid, aipmap.ReasonInvalidFieldMask},
	"fieldmask/update.ErrInvalidUpdateMask": {update.ErrInvalidUpdateMask, cInvalid, aipmap.ReasonInvalidFieldMask},
	"fieldmask.ErrLimitExceeded":            {fieldmask.ErrLimitExceeded, cInvalid, aipmap.ReasonFieldMaskTooComplex},
	"fieldmask.ErrMaxPathDepthExceeded":     {fieldmask.ErrMaxPathDepthExceeded, cInvalid, aipmap.ReasonFieldMaskTooComplex},
	"fieldmask.ErrMaxPathLengthExceeded":    {fieldmask.ErrMaxPathLengthExceeded, cInvalid, aipmap.ReasonFieldMaskTooComplex},
	"fieldmask.ErrMaxPathsExceeded":         {fieldmask.ErrMaxPathsExceeded, cInvalid, aipmap.ReasonFieldMaskTooComplex},
	"fieldmask/update.ErrMessageMismatch":   {update.ErrMessageMismatch, cInternal, rInternal},
	"fieldmask/update.ErrProtectedField":    {update.ErrProtectedField, cInvalid, aipmap.ReasonProtectedField},
	"fieldmask.ErrWildcardNotAlone":         {fieldmask.ErrWildcardNotAlone, cInvalid, aipmap.ReasonInvalidFieldMask},
	"fieldpolicy.ErrMalformedPattern":       {fieldpolicy.ErrMalformedPattern, cInternal, rInternal},
	"filter.ErrAllowlistRequired":           {filter.ErrAllowlistRequired, cInternal, rInternal},
	"filter.ErrDataLimitExceeded": {
		filter.ErrDataLimitExceeded, code.Code_RESOURCE_EXHAUSTED, aipmap.ReasonWorkBudgetExceeded,
	},
	"filter.ErrEmptyExpression":       {filter.ErrEmptyExpression, cInvalid, aipmap.ReasonInvalidFilter},
	"filter.ErrEnumValueNotAllowed":   {filter.ErrEnumValueNotAllowed, cInvalid, aipmap.ReasonInvalidFilter},
	"filter.ErrExpressionTooLong":     {filter.ErrExpressionTooLong, cInvalid, aipmap.ReasonFilterTooComplex},
	"filter.ErrFieldNotAllowed":       {filter.ErrFieldNotAllowed, cInvalid, aipmap.ReasonFilterNotAllowed},
	"filter.ErrFieldTypeMismatch":     {filter.ErrFieldTypeMismatch, cInvalid, aipmap.ReasonInvalidFilter},
	"filter.ErrFunctionNotAllowed":    {filter.ErrFunctionNotAllowed, cInvalid, aipmap.ReasonFilterNotAllowed},
	"filter.ErrInvalidExpression":     {filter.ErrInvalidExpression, cInvalid, aipmap.ReasonInvalidFilter},
	"filter.ErrInvalidOptions":        {filter.ErrInvalidOptions, cInternal, rInternal},
	"filter.ErrInvalidRegex":          {filter.ErrInvalidRegex, cInvalid, aipmap.ReasonInvalidFilter},
	"filter.ErrMaxDepthExceeded":      {filter.ErrMaxDepthExceeded, cInvalid, aipmap.ReasonFilterTooComplex},
	"filter.ErrMaxOperationsExceeded": {filter.ErrMaxOperationsExceeded, cInvalid, aipmap.ReasonFilterTooComplex},
	"filter.ErrParseFailed":           {filter.ErrParseFailed, cInvalid, aipmap.ReasonInvalidFilter},
	"filter.ErrPolicyRequired":        {filter.ErrPolicyRequired, cInternal, rInternal},
	"filter.ErrUnsupportedOperation":  {filter.ErrUnsupportedOperation, cInvalid, aipmap.ReasonInvalidFilter},
	"filter.ErrUnsupportedType":       {filter.ErrUnsupportedType, cInvalid, aipmap.ReasonInvalidFilter},
	"filter/regexanchor.ErrInvalidRegex": {
		regexanchor.ErrInvalidRegex, cInvalid, aipmap.ReasonInvalidFilter,
	},
	"listing.ErrInvalidRestriction":  {listing.ErrInvalidRestriction, cInternal, rInternal},
	"listing.ErrInvalidSchema":       {listing.ErrInvalidSchema, cInternal, rInternal},
	"listing.ErrUnreadableSortField": {listing.ErrUnreadableSortField, cInvalid, aipmap.ReasonOrderByNotAllowed},
	"listing.ErrWorkBudget": {
		listing.ErrWorkBudget, code.Code_RESOURCE_EXHAUSTED, aipmap.ReasonWorkBudgetExceeded,
	},
	// A stored NaN or infinity: a storage fault, INTERNAL as an unknown error.
	"listing/evaluation.ErrNonFiniteFloat":     {evaluation.ErrNonFiniteFloat, cInternal, rInternal},
	"orderby.ErrAllowlistRequired":             {orderby.ErrAllowlistRequired, cInternal, rInternal},
	"orderby.ErrDuplicateKey":                  {orderby.ErrDuplicateKey, cInvalid, aipmap.ReasonInvalidOrderBy},
	"orderby.ErrEmptyClause":                   {orderby.ErrEmptyClause, cInvalid, aipmap.ReasonInvalidOrderBy},
	"orderby.ErrExpressionTooLong":             {orderby.ErrExpressionTooLong, cInvalid, aipmap.ReasonOrderByTooComplex},
	"orderby.ErrFieldNotAllowed":               {orderby.ErrFieldNotAllowed, cInvalid, aipmap.ReasonOrderByNotAllowed},
	"orderby.ErrInvalidDirection":              {orderby.ErrInvalidDirection, cInvalid, aipmap.ReasonInvalidOrderBy},
	"orderby.ErrInvalidFieldPath":              {orderby.ErrInvalidFieldPath, cInvalid, aipmap.ReasonInvalidOrderBy},
	"orderby.ErrMaxFieldNameLengthExceeded":    {orderby.ErrMaxFieldNameLengthExceeded, cInvalid, aipmap.ReasonOrderByTooComplex},
	"orderby.ErrMaxFieldPathDepthExceeded":     {orderby.ErrMaxFieldPathDepthExceeded, cInvalid, aipmap.ReasonOrderByTooComplex},
	"orderby.ErrMaxKeysExceeded":               {orderby.ErrMaxKeysExceeded, cInvalid, aipmap.ReasonOrderByTooComplex},
	"orderby.ErrParseFailed":                   {orderby.ErrParseFailed, cInvalid, aipmap.ReasonInvalidOrderBy},
	"orderby.ErrPolicyRequired":                {orderby.ErrPolicyRequired, cInternal, rInternal},
	"orderby.ErrTooManySortKeys":               {orderby.ErrTooManySortKeys, cInvalid, aipmap.ReasonInvalidOrderBy},
	"orderby.ErrUnsupportedOrdering":           {orderby.ErrUnsupportedOrdering, cInvalid, aipmap.ReasonInvalidOrderBy},
	"pagination/pagetoken.ErrCodecRequired":    {pagetoken.ErrCodecRequired, cInvalid, aipmap.ReasonInvalidPageToken},
	"pagination/pagetoken.ErrExpiredToken":     {pagetoken.ErrExpiredToken, cInvalid, aipmap.ReasonExpiredPageToken},
	"pagination/pagetoken.ErrFilterChanged":    {pagetoken.ErrFilterChanged, cInvalid, aipmap.ReasonInvalidPageToken},
	"pagination/pagetoken.ErrInvalidToken":     {pagetoken.ErrInvalidToken, cInvalid, aipmap.ReasonInvalidPageToken},
	"pagination/pagetoken.ErrNegativeOffset":   {pagetoken.ErrNegativeOffset, cInternal, rInternal},
	"pagination/pagetoken.ErrPayloadTooLarge":  {pagetoken.ErrPayloadTooLarge, cInternal, rInternal},
	"pagination/pagetoken.ErrSortChanged":      {pagetoken.ErrSortChanged, cInvalid, aipmap.ReasonInvalidPageToken},
	"pagination/pagetoken.ErrSubjectMismatch":  {pagetoken.ErrSubjectMismatch, cInvalid, aipmap.ReasonInvalidPageToken},
	"pagination/pagetoken.ErrUnencryptedToken": {pagetoken.ErrUnencryptedToken, cInvalid, aipmap.ReasonInvalidPageToken},
	"pagination/pagetoken.ErrUnsortableValue":  {pagetoken.ErrUnsortableValue, cInternal, rInternal},
	"pagination/pagetoken.ErrWeakKey":          {pagetoken.ErrWeakKey, cInternal, rInternal},
	"projection.ErrAllFieldsUnresolvable":      {projection.ErrAllFieldsUnresolvable, cInternal, rInternal},
	"projection.ErrAllowlistRequired":          {projection.ErrAllowlistRequired, cInternal, rInternal},
	"projection.ErrConflictingPolicy":          {projection.ErrConflictingPolicy, cInternal, rInternal},
	"projection.ErrDuplicatePath":              {projection.ErrDuplicatePath, cInvalid, aipmap.ReasonInvalidReadMask},
	"projection.ErrEmptyClause":                {projection.ErrEmptyClause, cInvalid, aipmap.ReasonInvalidReadMask},
	"projection.ErrExpressionTooLong":          {projection.ErrExpressionTooLong, cInvalid, aipmap.ReasonReadMaskTooComplex},
	"projection.ErrFieldNotAllowed":            {projection.ErrFieldNotAllowed, cInvalid, aipmap.ReasonReadMaskNotAllowed},
	"projection.ErrInvalidFieldPath":           {projection.ErrInvalidFieldPath, cInvalid, aipmap.ReasonInvalidReadMask},
	"projection.ErrInvalidView":                {projection.ErrInvalidView, cInvalid, aipmap.ReasonInvalidReadMask},
	"projection.ErrMaxFieldNameLengthExceeded": {
		projection.ErrMaxFieldNameLengthExceeded, cInvalid, aipmap.ReasonReadMaskTooComplex,
	},
	"projection.ErrMaxFieldPathDepthExceeded": {
		projection.ErrMaxFieldPathDepthExceeded, cInvalid, aipmap.ReasonReadMaskTooComplex,
	},
	"projection.ErrMaxPathsExceeded":             {projection.ErrMaxPathsExceeded, cInvalid, aipmap.ReasonReadMaskTooComplex},
	"projection.ErrPolicyRequired":               {projection.ErrPolicyRequired, cInternal, rInternal},
	"projection.ErrUnsupportedPath":              {projection.ErrUnsupportedPath, cInvalid, aipmap.ReasonInvalidReadMask},
	"projection.ErrUnsupportedSelection":         {projection.ErrUnsupportedSelection, cInvalid, aipmap.ReasonInvalidReadMask},
	"projection/protopath.ErrDescriptorRequired": {protopath.ErrDescriptorRequired, cInternal, rInternal},
	"projection/protopath.ErrUnknownField":       {protopath.ErrUnknownField, cInvalid, aipmap.ReasonInvalidReadMask},
	"requestid.ErrInProgress":                    {requestid.ErrInProgress, code.Code_ABORTED, aipmap.ReasonRequestInProgress},
	"requestid.ErrInvalidRequestID":              {requestid.ErrInvalidRequestID, cInvalid, aipmap.ReasonInvalidRequestID},
	"requestid.ErrInvalidScope":                  {requestid.ErrInvalidScope, cInternal, rInternal},
	"requestid.ErrNoRequestIDField":              {requestid.ErrNoRequestIDField, cInternal, rInternal},
	// A bare sentinel carries no stored status: the rule's INTERNAL applies.
	"requestid.ErrReplayedFailure": {requestid.ErrReplayedFailure, cInternal, rInternal},
	"requestid.ErrRequestIDReused": {requestid.ErrRequestIDReused, code.Code_ALREADY_EXISTS, aipmap.ReasonRequestIDReused},
	"requestid.ErrReservationLost": {requestid.ErrReservationLost, cInternal, rInternal},
	"requestid.ErrResponseNotRetained": {
		requestid.ErrResponseNotRetained, code.Code_FAILED_PRECONDITION, aipmap.ReasonResponseNotRetained,
	},
	"requestid.ErrStore": {requestid.ErrStore, code.Code_UNAVAILABLE, apierror.ReasonUnavailable}, // ErrUnavailable
	// Do returns it wrapped in ErrStore.
	"requestid.ErrStoreFull": {
		fmt.Errorf("%w: %w", requestid.ErrStore, requestid.ErrStoreFull), code.Code_UNAVAILABLE, apierror.ReasonUnavailable,
	},
	"resourcename.ErrDuplicateType":     {resourcename.ErrDuplicateType, cInternal, rInternal},
	"resourcename.ErrInvalidFullName":   {resourcename.ErrInvalidFullName, cInvalid, aipmap.ReasonInvalidResourceName},
	"resourcename.ErrInvalidID":         {resourcename.ErrInvalidID, cInvalid, aipmap.ReasonInvalidResourceName},
	"resourcename.ErrInvalidName":       {resourcename.ErrInvalidName, cInvalid, aipmap.ReasonInvalidResourceName},
	"resourcename.ErrInvalidPattern":    {resourcename.ErrInvalidPattern, cInternal, rInternal},
	"resourcename.ErrInvalidResource":   {resourcename.ErrInvalidResource, cInternal, rInternal},
	"resourcename.ErrInvalidUID":        {resourcename.ErrInvalidUID, cInvalid, aipmap.ReasonInvalidUID},
	"resourcename.ErrInvalidValue":      {resourcename.ErrInvalidValue, cInternal, rInternal},
	"resourcename.ErrLimitExceeded":     {resourcename.ErrLimitExceeded, cInvalid, aipmap.ReasonInvalidResourceName},
	"resourcename.ErrNoMatch":           {resourcename.ErrNoMatch, cInvalid, aipmap.ReasonInvalidResourceName},
	"resourcename.ErrNoResource":        {resourcename.ErrNoResource, cInternal, rInternal},
	"resourcename.ErrUnknownType":       {resourcename.ErrUnknownType, cInternal, rInternal},
	"resourcename.ErrWildcard":          {resourcename.ErrWildcard, cInvalid, aipmap.ReasonInvalidResourceName},
	"softdelete.ErrAlreadyDeleted":      {softdelete.ErrAlreadyDeleted, code.Code_NOT_FOUND, apierror.ReasonNotFound},
	"softdelete.ErrFilterTooLong":       {softdelete.ErrFilterTooLong, cInvalid, aipmap.ReasonPurgeTooLarge},
	"softdelete.ErrInvalidPurger":       {softdelete.ErrInvalidPurger, cInternal, rInternal},
	"softdelete.ErrInvalidStoreResult":  {softdelete.ErrInvalidStoreResult, cInternal, rInternal},
	"softdelete.ErrNotDeleted":          {softdelete.ErrNotDeleted, code.Code_ALREADY_EXISTS, apierror.ReasonAlreadyExists},
	"softdelete.ErrNotFound":            {softdelete.ErrNotFound, code.Code_NOT_FOUND, apierror.ReasonNotFound},
	"softdelete.ErrTooManyResources":    {softdelete.ErrTooManyResources, cInvalid, aipmap.ReasonPurgeTooLarge},
	"softdelete.ErrUnsupportedResource": {softdelete.ErrUnsupportedResource, cInternal, rInternal},
	"state.ErrInvalidField":             {state.ErrInvalidField, cInternal, rInternal},
	"state.ErrInvalidTable":             {state.ErrInvalidTable, cInternal, rInternal},
	"state.ErrNotOutputOnly":            {state.ErrNotOutputOnly, cInternal, rInternal},
	"state.ErrTransitionNotAllowed": {
		state.ErrTransitionNotAllowed, code.Code_FAILED_PRECONDITION, aipmap.ReasonTransitionNotAllowed,
	},
	"unreachable.ErrInvalidName":   {unreachable.ErrInvalidName, cInternal, rInternal},
	"unreachable.ErrInvalidSource": {unreachable.ErrInvalidSource, cInternal, rInternal},
	"unreachable.ErrPartialSuccessUnsupported": {
		unreachable.ErrPartialSuccessUnsupported, cInvalid, aipmap.ReasonPartialSuccessUnsupported,
	},
	"unreachable.ErrUnreachable":   {unreachable.ErrUnreachable, code.Code_UNAVAILABLE, aipmap.ReasonResourcesUnreachable},
	"validateonly.ErrInvalidField": {validateonly.ErrInvalidField, cInternal, rInternal},
	"validateonly.ErrMissingStep":  {validateonly.ErrMissingStep, cInternal, rInternal},
}

// errorTypes lists every exported error type of the module outside
// internal packages, keyed like sentinels, with a value of the type.
var errorTypes = map[string]expectation{
	// An *apierror.Error passes through the mapper as it is.
	"apierror.Error": {apierror.MustNew(code.Code_NOT_FOUND, "gone"), code.Code_NOT_FOUND, "NOT_FOUND"},
	// An item error keeps the classification of its cause.
	"batch.ItemError": {
		&batch.ItemError{Field: "requests", Index: 1, Err: batch.ErrDuplicate}, cInvalid, aipmap.ReasonInvalidBatch,
	},
	"fieldbehavior.BehaviorViolationError": {
		&fieldbehavior.BehaviorViolationError{Violations: []fieldbehavior.BehaviorViolation{
			{Path: "name", Behavior: annotations.FieldBehavior_IMMUTABLE},
		}},
		cInvalid, aipmap.ReasonFieldBehaviorViolation,
	},
	"fieldinfo.ValueError": {&fieldinfo.ValueError{Format: "UUID4", Reason: "must be a UUID4"}, cInvalid, aipmap.ReasonInvalidFieldFormat},
	"fieldinfo.ViolationError": {
		&fieldinfo.ViolationError{Violations: []fieldinfo.Violation{{Path: "uid", Format: "UUID4", Reason: "must be a UUID4"}}},
		cInvalid, aipmap.ReasonInvalidFieldFormat,
	},
	// A mask path error keeps the classification of its cause.
	"fieldmask/update.MaskPathError": {&update.MaskPathError{Index: 0, Err: update.ErrProtectedField}, cInvalid, aipmap.ReasonProtectedField},
	"fieldmask/update.StrictError": {
		&update.StrictError{Violations: []update.FieldViolation{{Path: "nope", Reason: "unknown field"}}},
		cInvalid, aipmap.ReasonInvalidFieldMask,
	},
	"fieldmask/update.BehaviorError": {
		&update.BehaviorError{Violations: []update.FieldViolation{
			{Path: "title", Behavior: annotations.FieldBehavior_REQUIRED, Reason: "required"},
		}},
		cInvalid, aipmap.ReasonFieldBehaviorViolation,
	},
	"fieldmask.ValidationError": {&fieldmask.ValidationError{Path: "a..b", Reason: "empty segment"}, cInvalid, aipmap.ReasonInvalidFieldMask},
	"requestid.ReplayedError": {
		&requestid.ReplayedError{Status: &status.Status{Code: int32(code.Code_ABORTED), Message: "busy"}},
		code.Code_ABORTED, "ABORTED",
	},
	"resourcename.NameError": {
		&resourcename.NameError{Segment: 1, Reason: "bad", Cause: resourcename.ErrInvalidID}, cInvalid, aipmap.ReasonInvalidResourceName,
	},
	"state.TransitionError": {
		&state.TransitionError{From: "ACTIVE", To: "DELETED"}, code.Code_FAILED_PRECONDITION, aipmap.ReasonTransitionNotAllowed,
	},
	"unreachable.Error": {&unreachable.Error{Names: []string{"shelves/1"}}, code.Code_UNAVAILABLE, aipmap.ReasonResourcesUnreachable},
	// A source error keeps the classification of its cause.
	"unreachable.SourceError": {
		&unreachable.SourceError{Source: "eu", Err: coreerrs.ErrUnavailable}, code.Code_UNAVAILABLE, apierror.ReasonUnavailable,
	},
}

func TestSentinelMapping(t *testing.T) {
	t.Parallel()

	m := newMapper(t)

	for _, table := range []map[string]expectation{sentinels, errorTypes} {
		for _, name := range slices.Sorted(maps.Keys(table)) {
			tc := table[name]
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				e := m.Map(fmt.Errorf("op: %w", tc.err))
				assert.Equal(t, tc.code, e.Code())
				assert.Equal(t, tc.reason, e.Reason())

				if tc.code == cInternal {
					assert.Equal(t, apierror.MessageInternal, e.Message(), "a server fault never exposes its text")
				}
			})
		}
	}
}

// TestEverySentinelIsListed parses the module's core packages and fails for
// an exported Err* variable or error type that the tables above do not list,
// or for an entry that no longer exists. The sentinels of the backend modules
// are listed in the test-only module integration/crossbackend
// (TestEveryBackendSentinelIsListed), so that the root module requires no
// backend module.
func TestEverySentinelIsListed(t *testing.T) {
	t.Parallel()

	vars, types, err := testhelpers.ExportedErrors(filepath.Join("..", ".."), func(dir string) bool { return dir == "backend" })
	require.NoError(t, err)

	assert.ElementsMatch(t, slices.Sorted(maps.Keys(sentinels)), vars,
		"every exported sentinel needs an entry in sentinels (and a rule in aipmap, or a deliberate default)")
	assert.ElementsMatch(t, slices.Sorted(maps.Keys(errorTypes)), types,
		"every exported error type needs an entry in errorTypes")
}
