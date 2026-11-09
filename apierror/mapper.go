// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package apierror

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Messages and ErrorInfo reasons of the default rules ([DefaultRules]). They
// are part of the API contract: clients may match the reasons. They match the
// canonical rules of the go-atlas errstatus interceptor.
const (
	MessageNotFound           = "not found"
	MessageAlreadyExists      = "already exists"
	MessageConflict           = "changed concurrently, re-read and retry"
	MessageFailedPrecondition = "failed precondition"
	MessageUnavailable        = "temporarily unavailable, retry later"
	MessageBusy               = "the same work is in progress, retry later"
	MessageCanceled           = "the request was canceled"
	MessageDeadlineExceeded   = "the deadline expired before the operation could complete"
	MessageInternal           = "internal error"

	ReasonNotFound           = "NOT_FOUND"
	ReasonAlreadyExists      = "ALREADY_EXISTS"
	ReasonVersionConflict    = "VERSION_CONFLICT"
	ReasonInvalidArgument    = "INVALID_ARGUMENT"
	ReasonFailedPrecondition = "FAILED_PRECONDITION"
	ReasonUnavailable        = "TEMPORARY_UNAVAILABLE"
	ReasonBusy               = "BUSY"
	ReasonCanceled           = "CANCELLED" //nolint:misspell // the google.rpc.Code name
	ReasonDeadlineExceeded   = "DEADLINE_EXCEEDED"
	ReasonInternal           = "INTERNAL"
)

// MaxExposedMessageLength is the most runes of error text a [Mapper] copies
// into a status message or a field violation description: the text of an
// exposed error ([Rule.Expose], [WithExposeUnknown]) and the "<field>:
// <reason>" of a [coreerrs.FieldError]. Longer text is cut on a rune boundary
// and ends with [TruncatedSuffix], so the result is at most this many runes.
// The bound is defense in depth against an error that echoes a large client
// input; fixed messages and an [*Error] already in the chain are not cut.
const MaxExposedMessageLength = 1024

// TruncatedSuffix ends a message cut to [MaxExposedMessageLength].
const TruncatedSuffix = "…"

// BoundMessage returns s cut to [MaxExposedMessageLength] runes, ending with
// [TruncatedSuffix] when it was cut; shorter text is returned unchanged. A
// [Rule.Convert] that copies error text into a message can use it to apply
// the same bound as the [Mapper].
func BoundMessage(s string) string {
	if len(s) <= MaxExposedMessageLength || utf8.RuneCountInString(s) <= MaxExposedMessageLength {
		return s
	}

	end := 0
	for range MaxExposedMessageLength - utf8.RuneCountInString(TruncatedSuffix) {
		_, size := utf8.DecodeRuneInString(s[end:])
		end += size
	}

	return s[:end] + TruncatedSuffix
}

// Rule maps the errors that match Target (errors.Is) to a code and an
// ErrorInfo reason.
//
// The message is fixed: Message, or the default message of the code when
// empty, so the wrapped text — which may name stored data — never reaches the
// client. Expose publishes the error's text instead; set it only for errors
// that describe the client's own input. An INTERNAL rule never exposes. An
// exposed text longer than [MaxExposedMessageLength] runes is cut.
//
// When the code is INVALID_ARGUMENT and the error wraps a
// [coreerrs.FieldError], the message is "<field>: <reason>" and a BadRequest
// field violation names the field as written (the request's JSON path, such
// as "updateMask.paths[2]") with the rule's reason.
type Rule struct {
	// Target is the sentinel the error must match.
	Target error
	// Code is the google.rpc.Code of a matching error.
	Code code.Code
	// Reason is the ErrorInfo reason; empty is the code's name.
	Reason string
	// Message is the fixed message; empty is the code's default message.
	Message string
	// Expose publishes the error's text as the message.
	Expose bool
	// Convert, when set, builds the error itself — from a status the error
	// carries, say. A nil result falls back to Code, Reason and Message. The
	// mapper still fills in its default domain, and keeps the matched error
	// as a cause of a result that does not hold it already.
	Convert func(err error) *Error
}

// validate checks the rule is usable.
func (r Rule) validate() error {
	switch {
	case r.Target == nil:
		return fmt.Errorf("%w: rule without a target", ErrInvalidError)
	case !validCode(r.Code):
		return fmt.Errorf("%w: rule for %v: code %d is not an error code", ErrInvalidError, r.Target, r.Code)
	case r.Reason != "" && !ValidReason(r.Reason):
		return fmt.Errorf("%w: rule for %v: reason %q is not UPPER_SNAKE_CASE", ErrInvalidError, r.Target, r.Reason)
	case !utf8.ValidString(r.Message):
		return fmt.Errorf("%w: rule for %v: message is not UTF-8", ErrInvalidError, r.Target)
	}

	return nil
}

// DefaultRules returns the rules every [Mapper] applies after its own, in
// this order (the first match wins):
//
//	coreerrs.ErrNotFound            NOT_FOUND            "not found"                                  NOT_FOUND
//	coreerrs.ErrAlreadyExists       ALREADY_EXISTS       "already exists"                             ALREADY_EXISTS
//	coreerrs.ErrConflict            ABORTED              "changed concurrently, re-read and retry"    VERSION_CONFLICT
//	coreerrs.ErrInvalidArgument     INVALID_ARGUMENT     "<field>: <reason>" or the error's text      INVALID_ARGUMENT
//	coreerrs.ErrFailedPrecondition  FAILED_PRECONDITION  "failed precondition"                        FAILED_PRECONDITION
//	coreerrs.ErrUnavailable         UNAVAILABLE          "temporarily unavailable, retry later"       TEMPORARY_UNAVAILABLE
//	coreerrs.ErrBusy                ABORTED              "the same work is in progress, retry later"  BUSY
//	context.Canceled                (canceled)           "the request was canceled"                   [ReasonCanceled]
//	context.DeadlineExceeded        DEADLINE_EXCEEDED    "the deadline expired ..."                   DEADLINE_EXCEEDED
//
// A [coreerrs.FieldError] matches ErrInvalidArgument and adds its BadRequest
// field violation. ErrConflict is the AIP-154 etag mismatch (ABORTED);
// ErrAlreadyExists is a create that collides with an existing resource.
func DefaultRules() []Rule {
	return []Rule{
		{Target: coreerrs.ErrNotFound, Code: code.Code_NOT_FOUND, Message: MessageNotFound, Reason: ReasonNotFound},
		{Target: coreerrs.ErrAlreadyExists, Code: code.Code_ALREADY_EXISTS, Message: MessageAlreadyExists, Reason: ReasonAlreadyExists},
		{Target: coreerrs.ErrConflict, Code: code.Code_ABORTED, Message: MessageConflict, Reason: ReasonVersionConflict},
		{Target: coreerrs.ErrInvalidArgument, Code: code.Code_INVALID_ARGUMENT, Reason: ReasonInvalidArgument, Expose: true},
		{
			Target: coreerrs.ErrFailedPrecondition, Code: code.Code_FAILED_PRECONDITION, Message: MessageFailedPrecondition,
			Reason: ReasonFailedPrecondition,
		},
		{Target: coreerrs.ErrUnavailable, Code: code.Code_UNAVAILABLE, Message: MessageUnavailable, Reason: ReasonUnavailable},
		{Target: coreerrs.ErrBusy, Code: code.Code_ABORTED, Message: MessageBusy, Reason: ReasonBusy},
		{Target: context.Canceled, Code: code.Code_CANCELLED, Message: MessageCanceled, Reason: ReasonCanceled}, //nolint:misspell // google.rpc.Code
		{
			Target: context.DeadlineExceeded, Code: code.Code_DEADLINE_EXCEEDED, Message: MessageDeadlineExceeded,
			Reason: ReasonDeadlineExceeded,
		},
	}
}

// Mapper turns any error into an [Error]: an *Error in the chain as the
// error it maps to, otherwise by the first matching [Rule] — its own rules ([WithRules]), then
// [DefaultRules] — and an unmatched error as INTERNAL with
// [MessageInternal]. It fills in the domain ([WithDefaultDomain]) of an error
// without one. The mapped error keeps the original error as its cause, an
// *Error found in its chain or built by a [Rule.Convert] included, so
// [HiddenCause] reaches the cause a [Classified] error in it hides. A
// [Classified] error is classified by what it unwraps to alone, so the cause
// it hides selects no rule and is no part of a message. A Mapper is immutable
// and safe for concurrent use.
type Mapper struct {
	rules         []Rule
	domain        string
	exposeUnknown bool
}

// NewMapper returns a [Mapper]. It fails with [ErrInvalidError] for a rule
// without a target, with a code that is OK or not a google.rpc.Code, with a
// malformed reason or a message that is not UTF-8, and for a domain that is
// not UTF-8.
func NewMapper(opts ...MapperOption) (*Mapper, error) {
	o := newMapperOptions(opts...)

	for _, r := range o.rules {
		if err := r.validate(); err != nil {
			return nil, err
		}
	}

	if !utf8.ValidString(o.defaultDomain) {
		return nil, fmt.Errorf("%w: domain is not UTF-8", ErrInvalidError)
	}

	return &Mapper{
		rules:         append(slices.Clip(o.rules), DefaultRules()...),
		domain:        o.defaultDomain,
		exposeUnknown: o.exposeUnknown,
	}, nil
}

// defaultMapper applies [DefaultRules] only.
var defaultMapper = &Mapper{rules: DefaultRules()}

// FromError maps err with the default [Mapper]: [DefaultRules], no domain,
// unknown errors as INTERNAL without their text. It returns nil for nil.
func FromError(err error) *Error { return defaultMapper.Map(err) }

// Map returns the [Error] for err, nil for nil.
func (m *Mapper) Map(err error) *Error {
	if err == nil {
		return nil
	}

	if e, ok := errors.AsType[*Error](err); ok {
		return m.keepCause(e, err)
	}

	for _, r := range m.rules {
		if errors.Is(err, r.Target) {
			if r.Convert != nil {
				if e := r.Convert(err); e != nil {
					return m.keepCause(e, err)
				}
			}

			return m.build(err, r, r.Expose && r.Code != code.Code_INTERNAL)
		}
	}

	return m.build(err, Rule{Code: code.Code_INTERNAL, Reason: ReasonInternal}, m.exposeUnknown)
}

// keepCause returns e with the mapper's domain and err reachable from it:
// e itself when its tree holds err already (err is e, say), otherwise a copy
// whose cause is err, joined with e's own cause unless err's tree holds that
// one too. The copy is new, so no chain it is in can cycle back to it.
func (m *Mapper) keepCause(e *Error, err error) *Error {
	if holds(e, err) {
		return e.withDomain(m.domain)
	}

	c := e.clone()
	if m.domain != "" && c.info.GetDomain() == "" {
		c.info.Domain = m.domain
	}

	if e.cause == nil || holds(err, e.cause) {
		c.cause = err
	} else {
		c.cause = errors.Join(e.cause, err)
	}

	return c
}

// holds reports whether target is tree or an error tree unwraps to, in the
// order errors.Is walks it. Errors are compared as errors.Is compares them
// without their Is methods: by ==, and only when comparable.
func holds(tree, target error) bool {
	stack := []error{tree}
	for len(stack) > 0 {
		err := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if sameError(err, target) {
			return true
		}

		switch x := err.(type) {
		case interface{ Unwrap() error }:
			if inner := x.Unwrap(); inner != nil {
				stack = append(stack, inner)
			}
		case interface{ Unwrap() []error }:
			for _, inner := range slices.Backward(x.Unwrap()) {
				if inner != nil {
					stack = append(stack, inner)
				}
			}
		}
	}

	return false
}

// sameError reports whether a and b are the same error: of one type, both
// comparable, and equal by ==.
func sameError(a, b error) bool {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)

	return va.Type() == vb.Type() && va.Comparable() && vb.Comparable() && a == b
}

// build renders err under rule r, its text as the message when expose is set.
// The result keeps err as its cause.
func (m *Mapper) build(err error, r Rule, expose bool) *Error {
	reason := r.Reason
	if reason == "" {
		reason = r.Code.String()
	}

	msg := textOr(r.Message, defaultMessage(r.Code))
	if expose {
		msg = textOr(BoundMessage(err.Error()), msg)
	}

	var violations []*errdetails.BadRequest_FieldViolation

	if fe, ok := errors.AsType[*coreerrs.FieldError](err); ok && r.Code == code.Code_INVALID_ARGUMENT && fe.Field != "" {
		msg = textOr(BoundMessage(fe.Field+": "+fe.Reason), msg)
		violations = []*errdetails.BadRequest_FieldViolation{{Field: fe.Field, Description: BoundMessage(fe.Reason), Reason: reason}}
	}

	e, buildErr := New(r.Code, msg,
		WithReason(reason), WithDomain(m.domain), WithFieldViolations(violations...), WithCause(err))
	if buildErr != nil {
		// The exposed text or the field error is not UTF-8: fall back to the
		// fixed message, which NewMapper checked.
		e = MustNew(r.Code, textOr(r.Message, defaultMessage(r.Code)), WithReason(reason), WithDomain(m.domain),
			WithCause(err))
	}

	return e
}

// textOr returns s, or def when s is blank.
func textOr(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}

	return s
}

// defaultMessage is the message of a code without a fixed one: its name in
// lower case words ("invalid argument").
func defaultMessage(c code.Code) string {
	if c == code.Code_INTERNAL {
		return MessageInternal
	}

	return strings.ToLower(strings.ReplaceAll(c.String(), "_", " "))
}
