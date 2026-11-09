// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package apierror

import (
	"encoding/json"
	"fmt"
	"net/http"

	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/protobuf/encoding/protojson"
)

// statusClientClosedRequest is the HTTP status code.proto gives a canceled
// request (499 Client Closed Request, a de facto standard).
const statusClientClosedRequest = 499

// httpStatus maps each google.rpc.Code to the HTTP status code.proto names.
var httpStatus = [...]int{
	code.Code_OK:                  http.StatusOK,
	code.Code_CANCELLED:           statusClientClosedRequest, //nolint:misspell // the google.rpc.Code name
	code.Code_UNKNOWN:             http.StatusInternalServerError,
	code.Code_INVALID_ARGUMENT:    http.StatusBadRequest,
	code.Code_DEADLINE_EXCEEDED:   http.StatusGatewayTimeout,
	code.Code_NOT_FOUND:           http.StatusNotFound,
	code.Code_ALREADY_EXISTS:      http.StatusConflict,
	code.Code_PERMISSION_DENIED:   http.StatusForbidden,
	code.Code_RESOURCE_EXHAUSTED:  http.StatusTooManyRequests,
	code.Code_FAILED_PRECONDITION: http.StatusBadRequest,
	code.Code_ABORTED:             http.StatusConflict,
	code.Code_OUT_OF_RANGE:        http.StatusBadRequest,
	code.Code_UNIMPLEMENTED:       http.StatusNotImplemented,
	code.Code_INTERNAL:            http.StatusInternalServerError,
	code.Code_UNAVAILABLE:         http.StatusServiceUnavailable,
	code.Code_DATA_LOSS:           http.StatusInternalServerError,
	code.Code_UNAUTHENTICATED:     http.StatusUnauthorized,
}

// HTTPStatus returns the HTTP status code of c, as google/rpc/code.proto maps
// it (a canceled request is 499 Client Closed Request); a value outside
// google.rpc.Code is 500.
func HTTPStatus(c code.Code) int {
	if c < 0 || int(c) >= len(httpStatus) {
		return http.StatusInternalServerError
	}

	return httpStatus[c]
}

// HTTPStatus returns the HTTP status code of e's code.
func (e *Error) HTTPStatus() int { return HTTPStatus(e.code) }

// jsonError is the body of the AIP-193 HTTP/JSON form.
type jsonError struct {
	Error jsonStatus `json:"error"`
}

type jsonStatus struct {
	Code    int               `json:"code"`
	Message string            `json:"message"`
	Status  string            `json:"status"`
	Details []json.RawMessage `json:"details,omitempty"`
}

// JSON renders e in the AIP-193 HTTP/JSON form:
//
//	{"error": {"code": 404, "message": "...", "status": "NOT_FOUND",
//	  "details": [{"@type": "type.googleapis.com/google.rpc.ErrorInfo", ...}]}}
//
// code is the HTTP status ([Error.HTTPStatus]), status the google.rpc.Code
// name and each detail the protojson form of its Any. DebugInfo is left out
// unless [WithIncludeDebugInfo] asks for it. A detail kept packed whose type
// is not linked into the program fails to render.
func (e *Error) JSON(opts ...RenderOption) ([]byte, error) {
	s := e.Proto(opts...)

	body := jsonError{Error: jsonStatus{
		Code:    e.HTTPStatus(),
		Message: e.message,
		Status:  e.code.String(),
		Details: make([]json.RawMessage, 0, len(s.GetDetails())),
	}}

	for _, a := range s.GetDetails() {
		raw, err := protojson.Marshal(a)
		if err != nil {
			return nil, fmt.Errorf("apierror: render detail %s: %w", a.GetTypeUrl(), err)
		}

		body.Error.Details = append(body.Error.Details, raw)
	}

	return json.Marshal(body)
}

// MarshalJSON is [Error.JSON] with the default rendering, so json.Marshal of
// an *Error writes the AIP-193 HTTP/JSON form.
func (e *Error) MarshalJSON() ([]byte, error) { return e.JSON() }

// WriteHTTP writes e to w in the AIP-193 HTTP/JSON form with its HTTP status
// and a JSON content type. It returns the rendering or write error.
func (e *Error) WriteHTTP(w http.ResponseWriter, opts ...RenderOption) error {
	body, err := e.JSON(opts...)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(e.HTTPStatus())

	_, err = w.Write(body)

	return err
}
