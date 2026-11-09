// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package requestid_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/altessa-s/go-aip/requestid"

	fbpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	v1 "github.com/altessa-s/go-aip/internal/proto/gen/requestidtest/v1"
)

// cloneFingerprint is the reference fingerprint: a deep clone with the
// request ID and the ignored top-level fields cleared, deterministically
// encoded and hashed with the message type.
func cloneFingerprint(t testing.TB, req proto.Message, idField string, ignored ...string) []byte {
	t.Helper()

	c := proto.Clone(req).ProtoReflect()
	fields := c.Descriptor().Fields()

	for _, name := range append([]string{idField}, ignored...) {
		if fd := fields.ByName(protoreflect.Name(name)); fd != nil {
			c.Clear(fd)
		}
	}

	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(c.Interface())
	require.NoError(t, err)

	h := sha256.New()
	h.Write([]byte(c.Descriptor().FullName()))
	h.Write([]byte{0})
	h.Write(b)

	return h.Sum(nil)
}

// descriptorPayload is a large request with nested messages, repeated fields
// and set extensions: the descriptor of a test file whose field options carry
// google.api.field_behavior values.
func descriptorPayload() *descriptorpb.FileDescriptorProto {
	fdp := protodesc.ToFileDescriptorProto(fbpb.File_fieldbehaviortest_v1_test_proto)
	fdp.Name = new("f47ac10b-58cc-4372-a567-0e02b2c3d479")

	return fdp
}

// TestFingerprint_MatchesDeepClone pins the stored fingerprints: the shallow
// copy hashes the same bytes as the deep clone it replaced, for generated and
// dynamic messages, extensions, unknown fields, maps and ignored fields.
func TestFingerprint_MatchesDeepClone(t *testing.T) {
	t.Parallel()

	withUnknown := func() proto.Message {
		r := newRequest(testID)
		r.ProtoReflect().SetUnknown(protoreflect.RawFields{0xa8, 0x1f, 0x01}) // field 500, varint 1
		r.GetBook().ProtoReflect().SetUnknown(protoreflect.RawFields{0xb0, 0x1f, 0x02})

		return r
	}

	tests := []struct {
		name    string
		req     func() proto.Message
		idField string
		ignored []string
	}{
		{name: "create request", req: func() proto.Message { return newRequest(testID) }, idField: "request_id"},
		{name: "unset sub-message", req: func() proto.Message {
			return &v1.CreateBookRequest{Parent: "p", RequestId: testID}
		}, idField: "request_id"},
		{name: "unknown fields at every level", req: withUnknown, idField: "request_id"},
		{name: "ignored field", req: func() proto.Message { return newRequest(testID) }, idField: "request_id", ignored: []string{"parent", "undeclared"}},
		{name: "descriptor payload with extensions", req: func() proto.Message { return descriptorPayload() }, idField: "name"},
		{name: "descriptor payload, ignored repeated field", req: func() proto.Message { return descriptorPayload() }, idField: "name", ignored: []string{"extension", "options"}},
		{name: "dynamic message", req: func() proto.Message {
			d := dynamicpb.NewMessage(descriptorPayload().ProtoReflect().Descriptor())
			proto.Merge(d, descriptorPayload())

			return d
		}, idField: "name", ignored: []string{"package"}},
	}

	// The payload must exercise set extensions.
	opts := descriptorPayload().GetMessageType()[0].GetField()[0].GetOptions()
	require.True(t, proto.HasExtension(opts, annotations.E_FieldBehavior))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := []requestid.Option{requestid.WithField(tt.idField), requestid.WithFormat(requestid.FormatASCII)}
			if len(tt.ignored) > 0 {
				opts = append(opts, requestid.WithIgnoredFields(tt.ignored...))
			}

			g, err := requestid.New(requestid.NewMemoryStore(), opts...)
			require.NoError(t, err)

			req := tt.req()
			before := proto.Clone(req)

			got, err := requestid.Fingerprint(g, req)
			require.NoError(t, err)
			require.Equal(t, cloneFingerprint(t, req, tt.idField, tt.ignored...), got)
			require.True(t, proto.Equal(before, req), "the request is not modified")
		})
	}
}

// TestStoreKey_Stable pins the store key derivation, which stored entries are
// addressed by.
func TestStoreKey_Stable(t *testing.T) {
	t.Parallel()

	h := sha256.New()
	h.Write([]byte("requestid/v1"))

	for _, part := range []string{testScope.Method, testScope.Principal, testID} {
		h.Write([]byte{byte(len(part))})
		h.Write([]byte(part))
	}

	require.Equal(t, hex.EncodeToString(h.Sum(nil)), requestid.StoreKey(testScope, testID))
}

func BenchmarkFingerprint_DescriptorPayload(b *testing.B) {
	g, err := requestid.New(requestid.NewMemoryStore(), requestid.WithField("name"), requestid.WithFormat(requestid.FormatASCII))
	if err != nil {
		b.Fatal(err)
	}

	req := descriptorPayload()

	b.ReportAllocs()

	for b.Loop() {
		if _, err := requestid.Fingerprint(g, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFingerprint_CreateRequest(b *testing.B) {
	g, err := requestid.New(requestid.NewMemoryStore())
	if err != nil {
		b.Fatal(err)
	}

	req := newRequest(testID)

	b.ReportAllocs()

	for b.Loop() {
		if _, err := requestid.Fingerprint(g, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStoreKey(b *testing.B) {
	b.ReportAllocs()

	for b.Loop() {
		_ = requestid.StoreKey(testScope, testID)
	}
}
