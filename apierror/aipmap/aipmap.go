// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package aipmap

import (
	"errors"

	"google.golang.org/genproto/googleapis/rpc/code"

	"github.com/altessa-s/go-aip/apierror"
	"github.com/altessa-s/go-aip/batch"
	"github.com/altessa-s/go-aip/etag"
	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldinfo"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/extract"
	"github.com/altessa-s/go-aip/fieldmask/update"
	"github.com/altessa-s/go-aip/fieldpolicy"
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
	"github.com/altessa-s/go-aip/validateonly"
)

// ErrorInfo reasons of the rules. They are part of the API contract: clients
// may match them.
const (
	// ReasonInvalidFilter is a filter that does not parse or type-check (AIP-160).
	ReasonInvalidFilter = "INVALID_FILTER"
	// ReasonFilterNotAllowed is a filter over a field or function the policy does not allow.
	ReasonFilterNotAllowed = "FILTER_NOT_ALLOWED"
	// ReasonFilterTooComplex is a filter beyond the length, depth or operation limits.
	ReasonFilterTooComplex = "FILTER_TOO_COMPLEX"
	// ReasonInvalidOrderBy is an order_by that does not parse or that the backend cannot sort by (AIP-132).
	ReasonInvalidOrderBy = "INVALID_ORDER_BY"
	// ReasonOrderByNotAllowed is an order_by over a field the policy does not allow or the read mask omits.
	ReasonOrderByNotAllowed = "ORDER_BY_NOT_ALLOWED"
	// ReasonOrderByTooComplex is an order_by beyond the length, key or depth limits.
	ReasonOrderByTooComplex = "ORDER_BY_TOO_COMPLEX"
	// ReasonInvalidReadMask is a read mask or view that does not parse or that the backend cannot serve (AIP-157).
	ReasonInvalidReadMask = "INVALID_READ_MASK"
	// ReasonReadMaskNotAllowed is a read mask over a field the policy does not allow.
	ReasonReadMaskNotAllowed = "READ_MASK_NOT_ALLOWED"
	// ReasonReadMaskTooComplex is a read mask beyond the length, path-count or depth limits.
	ReasonReadMaskTooComplex = "READ_MASK_TOO_COMPLEX"
	// ReasonInvalidFieldMask is an update mask with a path that does not address a field (AIP-161, AIP-134).
	ReasonInvalidFieldMask = "INVALID_FIELD_MASK"
	// ReasonFieldMaskTooComplex is an update mask beyond the path-count, length or depth limits.
	ReasonFieldMaskTooComplex = "FIELD_MASK_TOO_COMPLEX"
	// ReasonFieldBehaviorViolation is a request that breaks a field behavior (AIP-203).
	ReasonFieldBehaviorViolation = "FIELD_BEHAVIOR_VIOLATION"
	// ReasonInvalidFieldFormat is a value that does not match the
	// google.api.field_info format of its field (AIP-202). Each BadRequest
	// field violation names the format: INVALID_<FORMAT> ([FormatReason]).
	ReasonInvalidFieldFormat = "INVALID_FIELD_FORMAT"
	// ReasonMessageTooDeep is a request whose messages nest deeper than the server walks.
	ReasonMessageTooDeep = "MESSAGE_TOO_DEEP"
	// ReasonEtagRequired is a request without the etag the method requires (AIP-154).
	ReasonEtagRequired = "ETAG_REQUIRED"
	// ReasonInvalidEtag is an etag that is not an entity tag, or not one the server issued (AIP-154).
	ReasonInvalidEtag = "INVALID_ETAG"
	// ReasonProtectedField is an update of a field that cannot be changed.
	ReasonProtectedField = "PROTECTED_FIELD"
	// ReasonElementCountMismatch is a "*" sub-path update whose list length differs from the stored one.
	ReasonElementCountMismatch = "ELEMENT_COUNT_MISMATCH"
	// ReasonInvalidPageToken is a page token that is malformed, forged, or bound to another query or caller (AIP-158).
	ReasonInvalidPageToken = "INVALID_PAGE_TOKEN"
	// ReasonExpiredPageToken is a page token past its lifetime (AIP-158).
	ReasonExpiredPageToken = "EXPIRED_PAGE_TOKEN"
	// ReasonWorkBudgetExceeded is a request that would spend more work than the server allows.
	ReasonWorkBudgetExceeded = "WORK_BUDGET_EXCEEDED"
	// ReasonInvalidBatch is a batch request that is empty or holds an item that contradicts the batch (AIP-231 to AIP-235).
	ReasonInvalidBatch = "INVALID_BATCH"
	// ReasonBatchTooLarge is a batch with more items than the server allows.
	ReasonBatchTooLarge = "BATCH_TOO_LARGE"
	// ReasonAllRequestsFailed is a partial-success batch in which no request succeeded (AIP-233 to AIP-235).
	ReasonAllRequestsFailed = "ALL_REQUESTS_FAILED"
	// ReasonPurgeTooLarge is a purge that matches more resources, or has a longer filter, than the server allows (AIP-165).
	ReasonPurgeTooLarge = "PURGE_TOO_LARGE"
	// ReasonInvalidRequestID is a request ID of the wrong format (AIP-155).
	ReasonInvalidRequestID = "INVALID_REQUEST_ID"
	// ReasonRequestIDReused is a request ID already used with other request parameters (AIP-155).
	ReasonRequestIDReused = "REQUEST_ID_REUSED"
	// ReasonRequestInProgress is a duplicate of a request that is still running (AIP-155).
	ReasonRequestInProgress = "REQUEST_IN_PROGRESS"
	// ReasonResponseNotRetained is a duplicate of a request whose response was not stored (AIP-155).
	ReasonResponseNotRetained = "RESPONSE_NOT_RETAINED"
	// ReasonResourcesUnreachable is a request that cannot succeed because resources are unreachable (AIP-217).
	ReasonResourcesUnreachable = "RESOURCES_UNREACHABLE"
	// ReasonPartialSuccessUnsupported is return_partial_success on a scope the server does not support (AIP-217).
	ReasonPartialSuccessUnsupported = "PARTIAL_SUCCESS_UNSUPPORTED"
	// ReasonTransitionNotAllowed is a state transition the current state does not allow (AIP-216).
	ReasonTransitionNotAllowed = "TRANSITION_NOT_ALLOWED"
	// ReasonInvalidResourceName is a resource name that is malformed, too long or of another pattern (AIP-122).
	ReasonInvalidResourceName = "INVALID_RESOURCE_NAME"
	// ReasonInvalidUID is a uid that is not a UUID4 (AIP-148).
	ReasonInvalidUID = "INVALID_UID"
)

// Rules returns the rules of the go-aip sentinels, for [apierror.WithRules]:
// client input errors are INVALID_ARGUMENT with their text exposed (it
// describes the request) and a reason above, an exhausted work budget is
// RESOURCE_EXHAUSTED, a stored list of another length FAILED_PRECONDITION,
// and configuration faults (a missing policy, an invalid schema, a weak key)
// INTERNAL with a fixed message. A FieldError around any of them — listing
// wraps them under filter, orderBy, pageToken or pageSize — keeps its field
// as the BadRequest field violation, except under INTERNAL: a configuration
// fault is never blamed on a request field.
//
// A replayed requestid failure becomes the stored status itself.
//
// An error that lists several fields — fieldinfo.ViolationError,
// fieldbehavior.BehaviorViolationError, update.BehaviorError
// and update.StrictError — gets one BadRequest field violation
// per listed path, with a reason per field behavior or format (see
// [BehaviorReason] and [FormatReason]); a FieldError around it (a batch item,
// say) prefixes every path with its field.
//
// The order matters where a sentinel matches another: the limit errors of
// fieldmask match fieldmask.ErrInvalidPath, a strict update-mask error
// carries update.ErrBehaviorViolation, and pagetoken.ErrUnencryptedToken
// wraps pagetoken.ErrInvalidToken.
//
// Sentinels without a rule keep the classification of [apierror.DefaultRules]:
// etag.ErrMismatch is a coreerrs.ErrConflict (ABORTED), batch.ErrNotFound,
// softdelete.ErrNotFound and ErrAlreadyDeleted are NOT_FOUND,
// softdelete.ErrNotDeleted ALREADY_EXISTS, requestid.ErrStore UNAVAILABLE,
// and the remaining server-side faults (backend/sqlbase.ErrScope and
// ErrCorruptRecord, requestid.ErrStoreFull on its own) INTERNAL as unknown
// errors. A stored record that does not decode is an [apierror.Classified]
// error: its decoder's cause is out of the chain and never selects a rule.
func Rules() []apierror.Rule {
	rules := make([]apierror.Rule, 0, 128)
	rules = append(rules, internalRules()...)
	rules = append(rules, fieldMaskRules()...)
	rules = append(rules, fieldInfoRules()...)
	rules = append(rules, etagRules()...)
	rules = append(rules, filterRules()...)
	rules = append(rules, orderByRules()...)
	rules = append(rules, projectionRules()...)

	rules = append(rules, pageTokenRules()...)
	rules = append(rules, batchRules()...)
	rules = append(rules, requestIDRules()...)
	rules = append(rules, unreachableRules()...)
	rules = append(rules, stateRules()...)

	return append(rules, resourceNameRules()...)
}

// internalRules are configuration faults: they win over everything, a
// FieldError around them included.
func internalRules() []apierror.Rule {
	return internal(
		listing.ErrInvalidSchema,
		filter.ErrAllowlistRequired, filter.ErrPolicyRequired, filter.ErrInvalidOptions,
		orderby.ErrAllowlistRequired, orderby.ErrPolicyRequired,
		projection.ErrAllowlistRequired, projection.ErrPolicyRequired, projection.ErrConflictingPolicy,
		projection.ErrAllFieldsUnresolvable, protopath.ErrDescriptorRequired,
		update.ErrMessageMismatch, update.ErrInvalidPatch, extract.ErrFieldNotSettable,
		fieldbehavior.ErrCurrentMismatch, fieldpolicy.ErrMalformedPattern,
		fieldinfo.ErrUnknownFormat, fieldinfo.ErrInvalidRegistry,
		etag.ErrNilMessage, etag.ErrNoField,
		validateonly.ErrInvalidField, validateonly.ErrMissingStep,
		pagetoken.ErrPayloadTooLarge, pagetoken.ErrWeakKey, pagetoken.ErrNegativeOffset, pagetoken.ErrUnsortableValue,
		batch.ErrNoTransaction, batch.ErrNotBatchRequest,
		softdelete.ErrUnsupportedResource, softdelete.ErrInvalidPurger, softdelete.ErrInvalidStoreResult,
		requestid.ErrNoRequestIDField, requestid.ErrInvalidScope, requestid.ErrReservationLost,
		unreachable.ErrInvalidName, unreachable.ErrInvalidSource,
		state.ErrInvalidTable, state.ErrInvalidField, state.ErrNotOutputOnly,
		resourcename.ErrInvalidPattern, resourcename.ErrInvalidResource, resourcename.ErrNoResource,
		resourcename.ErrUnknownType, resourcename.ErrDuplicateType, resourcename.ErrInvalidValue,
	)
}

// fieldMaskRules cover fieldmask and fieldbehavior. Every INVALID_ARGUMENT
// rule converts an error that lists violations into one field violation per
// path; any other error falls back to the rule itself.
func fieldMaskRules() []apierror.Rule {
	var rules []apierror.Rule

	rules = append(rules, withViolations(invalid(ReasonFieldBehaviorViolation,
		update.ErrBehaviorViolation, fieldbehavior.ErrBehaviorViolation))...)
	rules = append(rules, withViolations(invalid(ReasonProtectedField, update.ErrProtectedField))...)
	rules = append(rules, withViolations(invalid(ReasonFieldMaskTooComplex, fieldmask.ErrLimitExceeded))...)
	rules = append(rules, withViolations(invalid(ReasonInvalidFieldMask,
		fieldmask.ErrInvalidPath, fieldmask.ErrWildcardNotAlone, update.ErrInvalidUpdateMask))...)
	rules = append(rules, invalid(ReasonMessageTooDeep, fieldbehavior.ErrMaxDepthExceeded)...)

	return append(rules, apierror.Rule{
		Target: update.ErrElementCountMismatch, Code: code.Code_FAILED_PRECONDITION, Reason: ReasonElementCountMismatch,
		Message: "the list holds a different number of elements than the stored one; re-read the resource and retry",
	})
}

// fieldInfoRules cover the AIP-202 format checks of fieldinfo.
func fieldInfoRules() []apierror.Rule {
	rules := withViolations(invalid(ReasonInvalidFieldFormat, fieldinfo.ErrInvalidValue))

	return append(rules, invalid(ReasonMessageTooDeep, fieldinfo.ErrMaxDepthExceeded)...)
}

// etagRules cover the AIP-154 etag a client sends. A mismatch is
// coreerrs.ErrConflict, ABORTED by the default rules.
func etagRules() []apierror.Rule {
	rules := invalid(ReasonEtagRequired, etag.ErrMissing)

	return append(rules, invalid(ReasonInvalidEtag, etag.ErrInvalid, etag.ErrNotRevision)...)
}

func filterRules() []apierror.Rule {
	var rules []apierror.Rule

	rules = append(rules, invalid(ReasonFilterNotAllowed, filter.ErrFieldNotAllowed, filter.ErrFunctionNotAllowed)...)
	rules = append(rules, invalid(ReasonFilterTooComplex,
		filter.ErrMaxDepthExceeded, filter.ErrMaxOperationsExceeded, filter.ErrExpressionTooLong)...)
	rules = append(rules, invalid(ReasonInvalidFilter,
		filter.ErrParseFailed, filter.ErrInvalidExpression, filter.ErrEmptyExpression, filter.ErrUnsupportedOperation,
		filter.ErrUnsupportedType, filter.ErrFieldTypeMismatch, filter.ErrEnumValueNotAllowed, filter.ErrInvalidRegex)...)

	return append(rules, exhausted(filter.ErrDataLimitExceeded, listing.ErrWorkBudget)...)
}

func orderByRules() []apierror.Rule {
	var rules []apierror.Rule

	rules = append(rules, invalid(ReasonOrderByNotAllowed, orderby.ErrFieldNotAllowed, listing.ErrUnreadableSortField)...)
	rules = append(rules, invalid(ReasonOrderByTooComplex,
		orderby.ErrExpressionTooLong, orderby.ErrMaxKeysExceeded, orderby.ErrMaxFieldPathDepthExceeded,
		orderby.ErrMaxFieldNameLengthExceeded)...)

	return append(rules, invalid(ReasonInvalidOrderBy,
		orderby.ErrParseFailed, orderby.ErrEmptyClause, orderby.ErrInvalidFieldPath, orderby.ErrInvalidDirection,
		orderby.ErrDuplicateKey, orderby.ErrUnsupportedOrdering, orderby.ErrTooManySortKeys)...)
}

func projectionRules() []apierror.Rule {
	var rules []apierror.Rule

	rules = append(rules, invalid(ReasonReadMaskNotAllowed, projection.ErrFieldNotAllowed)...)
	rules = append(rules, invalid(ReasonReadMaskTooComplex,
		projection.ErrExpressionTooLong, projection.ErrMaxPathsExceeded, projection.ErrMaxFieldPathDepthExceeded,
		projection.ErrMaxFieldNameLengthExceeded)...)

	return append(rules, invalid(ReasonInvalidReadMask,
		projection.ErrDuplicatePath, projection.ErrUnsupportedSelection, projection.ErrEmptyClause,
		projection.ErrInvalidFieldPath, projection.ErrUnsupportedPath, projection.ErrInvalidView,
		protopath.ErrUnknownField)...)
}

func pageTokenRules() []apierror.Rule {
	var rules []apierror.Rule

	rules = append(rules, invalid(ReasonExpiredPageToken, pagetoken.ErrExpiredToken)...)

	return append(rules, invalid(ReasonInvalidPageToken,
		pagetoken.ErrInvalidToken, pagetoken.ErrSortChanged, pagetoken.ErrFilterChanged, pagetoken.ErrSubjectMismatch,
		pagetoken.ErrCodecRequired)...)
}

func batchRules() []apierror.Rule {
	var rules []apierror.Rule

	rules = append(rules, invalid(ReasonBatchTooLarge, batch.ErrTooManyItems)...)
	rules = append(rules, invalid(ReasonInvalidBatch,
		batch.ErrEmpty, batch.ErrParentMismatch, batch.ErrHoistedMismatch, batch.ErrDuplicate)...)
	rules = append(rules, invalid(ReasonPurgeTooLarge, softdelete.ErrTooManyResources, softdelete.ErrFilterTooLong)...)

	return append(rules, apierror.Rule{
		Target: batch.ErrAllFailed, Code: code.Code_ABORTED, Reason: ReasonAllRequestsFailed,
		Message: "none of the requests succeeded, refer to the operation metadata for more information",
	})
}

func requestIDRules() []apierror.Rule {
	return []apierror.Rule{
		{Target: requestid.ErrReplayedFailure, Code: code.Code_INTERNAL, Reason: apierror.ReasonInternal, Convert: replayed},
		{Target: requestid.ErrInvalidRequestID, Code: code.Code_INVALID_ARGUMENT, Reason: ReasonInvalidRequestID, Expose: true},
		{
			Target: requestid.ErrRequestIDReused, Code: code.Code_ALREADY_EXISTS, Reason: ReasonRequestIDReused,
			Message: "the request ID was already used with different request parameters",
		},
		{
			Target: requestid.ErrInProgress, Code: code.Code_ABORTED, Reason: ReasonRequestInProgress,
			Message: "a request with the same request ID is in progress, retry later with the same ID",
		},
		{
			Target: requestid.ErrResponseNotRetained, Code: code.Code_FAILED_PRECONDITION, Reason: ReasonResponseNotRetained,
			Message: "the request already succeeded but its response was not retained; read the resource",
		},
	}
}

// replayed returns the stored status of a requestid replay: the duplicate
// fails exactly as the original did. A stored status that is not AIP-193
// conformant (no ErrorInfo) keeps its code and message with the code's name
// as the reason; a status that is not an error falls back to the rule.
func replayed(err error) *apierror.Error {
	re, ok := errors.AsType[*requestid.ReplayedError](err)
	if !ok || re.Status == nil {
		return nil
	}

	if e, perr := apierror.FromProto(re.Status); perr == nil {
		return e
	}

	e, nerr := apierror.New(code.Code(re.Status.GetCode()), re.Status.GetMessage(), apierror.WithCause(err))
	if nerr != nil {
		return nil
	}

	return e
}

func unreachableRules() []apierror.Rule {
	return []apierror.Rule{
		{
			Target: unreachable.ErrPartialSuccessUnsupported, Code: code.Code_INVALID_ARGUMENT,
			Reason: ReasonPartialSuccessUnsupported, Expose: true,
		},
		{
			Target: unreachable.ErrUnreachable, Code: code.Code_UNAVAILABLE, Reason: ReasonResourcesUnreachable,
			Message: "some resources are unreachable, retry later or ask for partial success",
		},
	}
}

func stateRules() []apierror.Rule {
	return []apierror.Rule{{
		Target: state.ErrTransitionNotAllowed, Code: code.Code_FAILED_PRECONDITION, Reason: ReasonTransitionNotAllowed,
		Expose: true,
	}}
}

func resourceNameRules() []apierror.Rule {
	var rules []apierror.Rule

	rules = append(rules, invalid(ReasonInvalidUID, resourcename.ErrInvalidUID)...)

	return append(rules, invalid(ReasonInvalidResourceName, resourcename.ErrInvalidName)...)
}

// invalid returns INVALID_ARGUMENT rules exposing the error's text.
func invalid(reason string, targets ...error) []apierror.Rule {
	rules := make([]apierror.Rule, len(targets))
	for i, t := range targets {
		rules[i] = apierror.Rule{Target: t, Code: code.Code_INVALID_ARGUMENT, Reason: reason, Expose: true}
	}

	return rules
}

// exhausted returns RESOURCE_EXHAUSTED rules with a fixed message.
func exhausted(targets ...error) []apierror.Rule {
	rules := make([]apierror.Rule, len(targets))
	for i, t := range targets {
		rules[i] = apierror.Rule{
			Target: t, Code: code.Code_RESOURCE_EXHAUSTED, Reason: ReasonWorkBudgetExceeded,
			Message: "the request needs more work than the server allows; narrow the filter or the page",
		}
	}

	return rules
}

// internal returns INTERNAL rules.
func internal(targets ...error) []apierror.Rule {
	rules := make([]apierror.Rule, len(targets))
	for i, t := range targets {
		rules[i] = apierror.Rule{Target: t, Code: code.Code_INTERNAL, Reason: apierror.ReasonInternal}
	}

	return rules
}
