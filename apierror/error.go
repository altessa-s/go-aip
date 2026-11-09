// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package apierror

import (
	"fmt"
	"maps"

	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Error is an AIP-193 error: a google.rpc.Code, a developer-facing English
// message and the standard details, ErrorInfo always among them. It is
// immutable once built; a service returns it (or wraps it) like any error and
// a transport renders it with [Error.Proto], [Error.JSON] and
// [Error.HTTPStatus].
//
// errors.Is(err, target) holds for a target *Error with the same code and
// reason (a target without a reason matches the code alone), so a
// package-level *Error works as a sentinel; errors.Is and errors.As also reach
// the cause ([WithCause]).
type Error struct {
	code         code.Code
	message      string
	info         *errdetails.ErrorInfo
	badRequest   *errdetails.BadRequest
	localized    *errdetails.LocalizedMessage
	help         *errdetails.Help
	retry        *errdetails.RetryInfo
	quota        *errdetails.QuotaFailure
	precondition *errdetails.PreconditionFailure
	resource     *errdetails.ResourceInfo
	request      *errdetails.RequestInfo
	debug        *errdetails.DebugInfo
	extra        []proto.Message
	cause        error
}

// New builds an [Error] with code c and message, the details given by opts.
// The ErrorInfo reason defaults to the code's name. It fails with
// [ErrInvalidError] when c is OK or not a google.rpc.Code, the message is
// empty, the reason or a metadata key breaks the AIP-193 format, a
// LocalizedMessage lacks its locale or message, a field violation lacks its
// field, the retry delay is not positive, or a detail type is given twice.
func New(c code.Code, message string, opts ...Option) (*Error, error) {
	o := newErrorOptions(opts...)

	e := &Error{
		code:    c,
		message: message,
		info: &errdetails.ErrorInfo{
			Reason:   o.reason,
			Domain:   o.domain,
			Metadata: maps.Clone(o.metadata),
		},
		localized: o.localizedMessage,
		resource:  o.resourceInfo,
		request:   o.requestInfo,
		debug:     o.debugInfo,
		cause:     o.cause,
	}
	if e.info.GetReason() == "" {
		e.info.Reason = c.String()
	}

	if len(o.fieldViolations) > 0 {
		e.badRequest = &errdetails.BadRequest{FieldViolations: o.fieldViolations}
	}

	if len(o.helpLinks) > 0 {
		e.help = &errdetails.Help{Links: o.helpLinks}
	}

	if o.retryDelay != 0 {
		e.retry = &errdetails.RetryInfo{RetryDelay: durationpb.New(o.retryDelay)}
	}

	if len(o.quotaViolations) > 0 {
		e.quota = &errdetails.QuotaFailure{Violations: o.quotaViolations}
	}

	if len(o.preconditionViolations) > 0 {
		e.precondition = &errdetails.PreconditionFailure{Violations: o.preconditionViolations}
	}

	if err := e.addExtra(o.details); err != nil {
		return nil, err
	}

	if err := e.validate(); err != nil {
		return nil, err
	}

	return e.clone(), nil
}

// MustNew is [New] for package-level errors: it panics on an invalid one.
func MustNew(c code.Code, message string, opts ...Option) *Error {
	e, err := New(c, message, opts...)
	if err != nil {
		panic(err)
	}

	return e
}

// Error renders "<CODE>: <message>", the code by its google.rpc.Code name.
func (e *Error) Error() string { return e.code.String() + ": " + e.message }

// Unwrap returns the cause, if any.
func (e *Error) Unwrap() error { return e.cause }

// Is reports whether target is an *Error with the same code and, when the
// target has a reason, the same reason.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok || t == nil {
		return false
	}

	return t.code == e.code && (t.info.GetReason() == "" || t.info.GetReason() == e.info.GetReason())
}

// Code returns the google.rpc.Code.
func (e *Error) Code() code.Code { return e.code }

// Message returns the developer-facing message.
func (e *Error) Message() string { return e.message }

// Reason returns the ErrorInfo reason.
func (e *Error) Reason() string { return e.info.GetReason() }

// Domain returns the ErrorInfo domain.
func (e *Error) Domain() string { return e.info.GetDomain() }

// Metadata returns a copy of the ErrorInfo metadata.
func (e *Error) Metadata() map[string]string { return maps.Clone(e.info.GetMetadata()) }

// FieldViolations returns copies of the BadRequest field violations.
func (e *Error) FieldViolations() []*errdetails.BadRequest_FieldViolation {
	return cloneAll(e.badRequest.GetFieldViolations())
}

// Details returns copies of the detail messages in their rendering order,
// ErrorInfo first; DebugInfo only with [WithIncludeDebugInfo].
func (e *Error) Details(opts ...RenderOption) []proto.Message {
	return cloneAll(e.details(newRenderOptions(opts...)))
}

// details lists the detail messages without copying them.
func (e *Error) details(o *renderOptions) []proto.Message {
	list := make([]proto.Message, 0, 4+len(e.extra))
	list = append(list, e.info)

	for _, m := range []proto.Message{
		e.badRequest, e.precondition, e.quota, e.resource, e.retry, e.request, e.help, e.localized,
	} {
		if !isNil(m) {
			list = append(list, m)
		}
	}

	if o.includeDebugInfo && e.debug != nil {
		list = append(list, e.debug)
	}

	return append(list, e.extra...)
}

// withDomain returns e, or a copy of e with the domain when e has none.
func (e *Error) withDomain(domain string) *Error {
	if domain == "" || e.info.GetDomain() != "" {
		return e
	}

	c := e.clone()
	c.info.Domain = domain

	return c
}

// clone returns a deep copy of e, so later changes to the messages given to
// [New] do not reach it.
func (e *Error) clone() *Error {
	c := *e
	c.info = cloneMsg(e.info)
	c.badRequest = cloneMsg(e.badRequest)
	c.localized = cloneMsg(e.localized)
	c.help = cloneMsg(e.help)
	c.retry = cloneMsg(e.retry)
	c.quota = cloneMsg(e.quota)
	c.precondition = cloneMsg(e.precondition)
	c.resource = cloneMsg(e.resource)
	c.request = cloneMsg(e.request)
	c.debug = cloneMsg(e.debug)
	c.extra = cloneAll(e.extra)

	return &c
}

// addExtra files each message of details in its standard slot or, for any
// other type, in extra; a type given twice is an error.
func (e *Error) addExtra(details []proto.Message) error {
	for _, m := range details {
		if isNil(m) {
			return fmt.Errorf("%w: nil detail", ErrInvalidError)
		}

		if err := e.put(m); err != nil {
			return err
		}
	}

	return nil
}

// put files m in its slot, failing when the slot is taken.
func (e *Error) put(m proto.Message) error {
	dup := false

	switch d := m.(type) {
	case *errdetails.ErrorInfo:
		// FromProto only: New always has its own ErrorInfo.
		dup = e.info != nil
		e.info = d
	case *errdetails.BadRequest:
		dup = e.badRequest != nil
		e.badRequest = d
	case *errdetails.LocalizedMessage:
		dup = e.localized != nil
		e.localized = d
	case *errdetails.Help:
		dup = e.help != nil
		e.help = d
	case *errdetails.RetryInfo:
		dup = e.retry != nil
		e.retry = d
	case *errdetails.QuotaFailure:
		dup = e.quota != nil
		e.quota = d
	case *errdetails.PreconditionFailure:
		dup = e.precondition != nil
		e.precondition = d
	case *errdetails.ResourceInfo:
		dup = e.resource != nil
		e.resource = d
	case *errdetails.RequestInfo:
		dup = e.request != nil
		e.request = d
	case *errdetails.DebugInfo:
		dup = e.debug != nil
		e.debug = d
	default:
		key := detailKey(m)
		for _, x := range e.extra {
			if detailKey(x) == key {
				dup = true
			}
		}

		e.extra = append(e.extra, m)
	}

	if dup {
		return fmt.Errorf("%w: detail %s given more than once", ErrInvalidError, detailKey(m))
	}

	return nil
}

// detailKey identifies the type of a detail: its full name, or the type URL
// of a detail kept packed.
func detailKey(m proto.Message) string {
	if a, ok := m.(*anypb.Any); ok {
		return a.GetTypeUrl()
	}

	return string(m.ProtoReflect().Descriptor().FullName())
}

func cloneMsg[M proto.Message](m M) M {
	if isNil(m) {
		return m
	}

	return proto.Clone(m).(M) //nolint:forcetypeassert,errcheck // proto.Clone keeps the concrete type
}

func cloneAll[M proto.Message](list []M) []M {
	if list == nil {
		return nil
	}

	out := make([]M, len(list))
	for i, m := range list {
		out[i] = cloneMsg(m)
	}

	return out
}

// isNil reports whether m is nil or a typed nil message.
func isNil(m proto.Message) bool {
	return m == nil || !m.ProtoReflect().IsValid()
}
