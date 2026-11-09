// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package apierror_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/apierror"
)

func sampleError(t *testing.T) *apierror.Error {
	t.Helper()

	e, err := apierror.New(code.Code_NOT_FOUND, "shelf not found",
		apierror.WithReason("SHELF_NOT_FOUND"), apierror.WithDomain("library.example.com"),
		apierror.WithMetadata(map[string]string{"shelf": "shelves/1"}),
		apierror.WithResourceInfo(&errdetails.ResourceInfo{ResourceType: "Shelf", ResourceName: "shelves/1"}),
		apierror.WithDebugInfo(&errdetails.DebugInfo{Detail: "secret stack"}))
	require.NoError(t, err)

	return e
}

func TestProto(t *testing.T) {
	t.Parallel()

	e := sampleError(t)
	s := e.Proto()
	assert.Equal(t, int32(code.Code_NOT_FOUND), s.GetCode())
	assert.Equal(t, "shelf not found", s.GetMessage())
	require.Len(t, s.GetDetails(), 2, "ErrorInfo and ResourceInfo; no DebugInfo")

	info := &errdetails.ErrorInfo{}
	require.NoError(t, s.GetDetails()[0].UnmarshalTo(info))
	assert.Equal(t, "SHELF_NOT_FOUND", info.GetReason())
	assert.Equal(t, "library.example.com", info.GetDomain())

	assert.True(t, proto.Equal(s, e.Status()))
	require.Len(t, e.Proto(apierror.WithIncludeDebugInfo(true)).GetDetails(), 3)
}

func TestFromProtoRoundTrip(t *testing.T) {
	t.Parallel()

	e := sampleError(t)
	s := e.Proto(apierror.WithIncludeDebugInfo(true))

	back, err := apierror.FromProto(s)
	require.NoError(t, err)
	assert.Equal(t, e.Code(), back.Code())
	assert.Equal(t, e.Message(), back.Message())
	assert.Equal(t, e.Reason(), back.Reason())
	assert.True(t, proto.Equal(s, back.Proto(apierror.WithIncludeDebugInfo(true))))
}

func TestFromProtoKeepsUnknownDetailsPacked(t *testing.T) {
	t.Parallel()

	info, err := anypb.New(&errdetails.ErrorInfo{Reason: "X_Y", Domain: "d"})
	require.NoError(t, err)

	unknownA := &anypb.Any{TypeUrl: "type.googleapis.com/example.Unknown", Value: []byte{}}
	unknownB := &anypb.Any{TypeUrl: "type.googleapis.com/example.Other", Value: []byte{}}
	s := &status.Status{Code: int32(code.Code_ABORTED), Message: "m", Details: []*anypb.Any{info, unknownA, unknownB}}

	e, err := apierror.FromProto(s)
	require.NoError(t, err)
	assert.True(t, proto.Equal(s, e.Proto()))

	_, err = e.JSON()
	require.Error(t, err, "an unresolvable Any does not render as JSON")

	s.Details = append(s.Details, unknownA)
	_, err = apierror.FromProto(s)
	require.ErrorIs(t, err, apierror.ErrInvalidError, "a type given twice")
}

func TestFromProtoInvalid(t *testing.T) {
	t.Parallel()

	info, err := anypb.New(&errdetails.ErrorInfo{Reason: "X_Y"})
	require.NoError(t, err)

	for name, s := range map[string]*status.Status{
		"nil":          nil,
		"OK":           {Code: 0, Message: "m"},
		"no ErrorInfo": {Code: int32(code.Code_INTERNAL), Message: "m"},
		"no message":   {Code: int32(code.Code_INTERNAL), Details: []*anypb.Any{info}},
		"two infos":    {Code: int32(code.Code_INTERNAL), Message: "m", Details: []*anypb.Any{info, info}},
	} {
		_, err := apierror.FromProto(s)
		require.ErrorIs(t, err, apierror.ErrInvalidError, name)
	}
}

func TestHTTPStatus(t *testing.T) {
	t.Parallel()

	want := map[code.Code]int{
		code.Code_OK: 200, code.Code_CANCELLED: 499, code.Code_UNKNOWN: 500, code.Code_INVALID_ARGUMENT: 400, //nolint:misspell // google.rpc.Code
		code.Code_DEADLINE_EXCEEDED: 504, code.Code_NOT_FOUND: 404, code.Code_ALREADY_EXISTS: 409,
		code.Code_PERMISSION_DENIED: 403, code.Code_UNAUTHENTICATED: 401, code.Code_RESOURCE_EXHAUSTED: 429,
		code.Code_FAILED_PRECONDITION: 400, code.Code_ABORTED: 409, code.Code_OUT_OF_RANGE: 400,
		code.Code_UNIMPLEMENTED: 501, code.Code_INTERNAL: 500, code.Code_UNAVAILABLE: 503, code.Code_DATA_LOSS: 500,
		code.Code(-1): 500, code.Code(42): 500,
	}
	for c, s := range want {
		assert.Equal(t, s, apierror.HTTPStatus(c), c.String())
	}

	assert.Equal(t, 404, sampleError(t).HTTPStatus())
}

func TestJSON(t *testing.T) {
	t.Parallel()

	e := sampleError(t)
	raw, err := json.Marshal(e)
	require.NoError(t, err)

	var body struct {
		Error struct {
			Code    int              `json:"code"`
			Message string           `json:"message"`
			Status  string           `json:"status"`
			Details []map[string]any `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	assert.Equal(t, 404, body.Error.Code, "code is the HTTP status")
	assert.Equal(t, "NOT_FOUND", body.Error.Status)
	assert.Equal(t, "shelf not found", body.Error.Message)
	require.Len(t, body.Error.Details, 2)
	assert.Equal(t, "type.googleapis.com/google.rpc.ErrorInfo", body.Error.Details[0]["@type"])
	assert.Equal(t, "SHELF_NOT_FOUND", body.Error.Details[0]["reason"])
	assert.Equal(t, map[string]any{"shelf": "shelves/1"}, body.Error.Details[0]["metadata"])
	assert.Equal(t, "type.googleapis.com/google.rpc.ResourceInfo", body.Error.Details[1]["@type"])
	assert.Equal(t, "shelves/1", body.Error.Details[1]["resourceName"], "protojson field names")
	assert.NotContains(t, string(raw), "secret stack")

	withDebug, err := e.JSON(apierror.WithIncludeDebugInfo(true))
	require.NoError(t, err)
	assert.Contains(t, string(withDebug), "secret stack")
}

func TestWriteHTTP(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	require.NoError(t, sampleError(t).WriteHTTP(rec))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), `"status":"NOT_FOUND"`)

	e, err := apierror.FromProto(&status.Status{Code: int32(code.Code_INTERNAL), Message: "m", Details: []*anypb.Any{
		mustAny(t, &errdetails.ErrorInfo{Reason: "X_Y"}), {TypeUrl: "type.googleapis.com/example.Unknown"},
	}})
	require.NoError(t, err)

	rec = httptest.NewRecorder()
	require.Error(t, e.WriteHTTP(rec))
	assert.Equal(t, http.StatusOK, rec.Code, "nothing written on a rendering error")

	require.ErrorIs(t, sampleError(t).WriteHTTP(failingWriter{httptest.NewRecorder()}), errWrite)
}

var errWrite = errors.New("write failed")

type failingWriter struct{ *httptest.ResponseRecorder }

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestDetailsWithExtraStruct(t *testing.T) {
	t.Parallel()

	st, err := structpb.NewStruct(map[string]any{"a": "b"})
	require.NoError(t, err)

	e := apierror.MustNew(code.Code_INTERNAL, "x", apierror.WithDetails(st))
	raw, err := e.JSON()
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"@type":"type.googleapis.com/google.protobuf.Struct"`)
}

func mustAny(t *testing.T, m proto.Message) *anypb.Any {
	t.Helper()

	a, err := anypb.New(m)
	require.NoError(t, err)

	return a
}
