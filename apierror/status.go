// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package apierror

import (
	"fmt"

	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// Proto renders e as a google.rpc.Status, each detail packed in an Any,
// ErrorInfo first. DebugInfo is left out unless [WithIncludeDebugInfo] asks
// for it. A gRPC layer returns status.FromProto(e.Proto()).Err().
func (e *Error) Proto(opts ...RenderOption) *status.Status {
	details := e.details(newRenderOptions(opts...))

	s := &status.Status{Code: int32(e.code), Message: e.message, Details: make([]*anypb.Any, 0, len(details))}
	for _, d := range details {
		a, err := pack(d)
		if err != nil {
			continue // unreachable: New and FromProto check that every detail marshals
		}

		s.Details = append(s.Details, a)
	}

	return s
}

// Status is [Error.Proto] with the default rendering: the google.rpc.Status a
// client may see.
func (e *Error) Status() *status.Status { return e.Proto() }

// FromProto reads a google.rpc.Status back into an [Error], for a client or a
// proxy. A detail that does not unpack (its type is not linked into the
// program) stays packed as Any. It fails with [ErrInvalidError] for a nil or
// OK status, a detail type given twice or a status that breaks AIP-193 (no
// ErrorInfo among them, say).
func FromProto(s *status.Status) (*Error, error) {
	if s == nil || s.GetCode() == int32(code.Code_OK) {
		return nil, fmt.Errorf("%w: status is nil or OK", ErrInvalidError)
	}

	e := &Error{code: code.Code(s.GetCode()), message: s.GetMessage()}

	for _, a := range s.GetDetails() {
		m, err := a.UnmarshalNew()
		if err != nil {
			m = a
		}

		if err := e.put(m); err != nil {
			return nil, err
		}
	}

	if err := e.validate(); err != nil {
		return nil, err
	}

	return e.clone(), nil
}

// pack wraps d in an Any; a detail kept packed is returned as it is.
func pack(d proto.Message) (*anypb.Any, error) {
	if a, ok := d.(*anypb.Any); ok {
		return a, nil
	}

	return anypb.New(d)
}
