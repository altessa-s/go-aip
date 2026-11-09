// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package protopath_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/altessa-s/go-aip/projection"
	"github.com/altessa-s/go-aip/projection/protopath"

	_ "google.golang.org/protobuf/types/known/anypb"
	_ "google.golang.org/protobuf/types/known/durationpb"
	_ "google.golang.org/protobuf/types/known/timestamppb"
	_ "google.golang.org/protobuf/types/known/wrapperspb"

	fieldbehaviortestv1 "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	fieldmasktestv1 "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
)

func mustValidator(tb testing.TB, md protoreflect.MessageDescriptor, opts ...protopath.Option) *protopath.Validator {
	tb.Helper()

	v, err := protopath.New(md, opts...)
	require.NoError(tb, err)

	return v
}

// wktHolder builds a message with Timestamp, Duration, Any and wrapper fields
// from a descriptor, so no fixture file is needed for them.
func wktHolder(tb testing.TB) protoreflect.MessageDescriptor {
	tb.Helper()

	msgField := func(name string, num int32, typ string) *descriptorpb.FieldDescriptorProto {
		return &descriptorpb.FieldDescriptorProto{
			Name:     new(name),
			Number:   new(num),
			Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
			TypeName: new(typ),
			JsonName: new(name),
		}
	}

	fdp := &descriptorpb.FileDescriptorProto{
		Name:    new("protopath_wkt_test.proto"),
		Package: new("protopathtest"),
		Syntax:  new("proto3"),
		Dependency: []string{
			"google/protobuf/any.proto",
			"google/protobuf/duration.proto",
			"google/protobuf/timestamp.proto",
			"google/protobuf/wrappers.proto",
		},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: new("Holder"),
			Field: []*descriptorpb.FieldDescriptorProto{
				msgField("created", 1, ".google.protobuf.Timestamp"),
				msgField("ttl", 2, ".google.protobuf.Duration"),
				msgField("payload", 3, ".google.protobuf.Any"),
				msgField("count", 4, ".google.protobuf.Int64Value"),
			},
		}},
	}

	fd, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	require.NoError(tb, err)

	return fd.Messages().ByName("Holder")
}

func TestNewRequiresDescriptor(t *testing.T) {
	t.Parallel()

	_, err := protopath.New(nil)
	require.ErrorIs(t, err, protopath.ErrDescriptorRequired)
}

func TestValidate(t *testing.T) {
	t.Parallel()

	account := (&fieldbehaviortestv1.Account{}).ProtoReflect().Descriptor()
	resource := (&fieldbehaviortestv1.Resource{}).ProtoReflect().Descriptor()
	getReq := (&fieldbehaviortestv1.GetResourceRequest{}).ProtoReflect().Descriptor()
	update := (&fieldmasktestv1.UpdateRequest{}).ProtoReflect().Descriptor()
	holder := wktHolder(t)

	tests := []struct {
		name    string
		md      protoreflect.MessageDescriptor
		opts    []protopath.Option
		path    string
		wantErr []error
	}{
		// Valid paths.
		{name: "top-level scalar", md: account, path: "display_name"},
		{name: "enum leaf", md: account, path: "state"},
		{name: "optional scalar", md: account, path: "quota"},
		{name: "oneof member", md: account, path: "card"},
		{name: "whole message", md: account, path: "owner"},
		{name: "nested scalar", md: account, path: "owner.email"},
		{name: "repeated scalar terminal", md: account, path: "emails"},
		{name: "repeated message terminal", md: account, path: "members"},
		{name: "map terminal", md: account, path: "delegates"},
		{name: "recursive message", md: account, path: "parent.parent.owner.note"},
		{name: "recursive self-typed field", md: account, path: "settings.defaults.defaults.theme"},
		{name: "wkt whole", md: update, path: "extra_data"},
		{name: "field mask whole", md: getReq, path: "read_mask"},
		{name: "timestamp whole", md: holder, path: "created"},

		// Repeated fields are terminal.
		{
			name: "index access", md: resource, path: "aliases.0",
			wantErr: []error{projection.ErrInvalidFieldPath},
		},
		{
			name: "repeated message traversal", md: account, path: "members.email",
			wantErr: []error{projection.ErrInvalidFieldPath},
		},
		{
			name: "repeated wildcard", md: account, path: "members.*.email",
			wantErr: []error{projection.ErrInvalidFieldPath},
		},
		{
			name: "repeated scalar traversal", md: account, path: "emails.domain",
			wantErr: []error{projection.ErrInvalidFieldPath},
		},
		{
			name: "repeated wkt traversal", md: update, path: "struct_list.fields",
			wantErr: []error{projection.ErrInvalidFieldPath},
		},

		// Maps are terminal.
		{
			name: "map key", md: account, path: "attributes.region",
			wantErr: []error{projection.ErrUnsupportedPath},
		},
		{
			name: "map value field", md: resource, path: "labels.k.display_name",
			wantErr: []error{projection.ErrUnsupportedPath},
		},
		{
			name: "map quoted key", md: resource, path: "labels.`a.b`",
			wantErr: []error{projection.ErrUnsupportedPath},
		},
		{
			name: "map wildcard", md: account, path: "delegates.*.email",
			wantErr: []error{projection.ErrUnsupportedPath},
		},
		{
			name: "map malformed key", md: account, path: "attributes.a-b",
			wantErr: []error{projection.ErrInvalidFieldPath},
		},

		// Scalars and well-known types are leaves.
		{
			name: "scalar subfield", md: account, path: "display_name.first",
			wantErr: []error{projection.ErrInvalidFieldPath},
		},
		{
			name: "enum subfield", md: account, path: "state.number",
			wantErr: []error{projection.ErrInvalidFieldPath},
		},
		{
			name: "struct subfield", md: update, path: "extra_data.fields",
			wantErr: []error{projection.ErrInvalidFieldPath},
		},
		{
			name: "value subfield", md: update, path: "value_data.string_value",
			wantErr: []error{projection.ErrInvalidFieldPath},
		},
		{
			name: "list value subfield", md: update, path: "list_data.values",
			wantErr: []error{projection.ErrInvalidFieldPath},
		},
		{
			name: "field mask subfield", md: getReq, path: "read_mask.paths",
			wantErr: []error{projection.ErrInvalidFieldPath},
		},
		{
			name: "timestamp subfield", md: holder, path: "created.seconds",
			wantErr: []error{projection.ErrInvalidFieldPath},
		},
		{
			name: "duration subfield", md: holder, path: "ttl.nanos",
			wantErr: []error{projection.ErrInvalidFieldPath},
		},
		{
			name: "any subfield", md: holder, path: "payload.type_url",
			wantErr: []error{projection.ErrInvalidFieldPath},
		},
		{
			name: "wrapper subfield", md: holder, path: "count.value",
			wantErr: []error{projection.ErrInvalidFieldPath},
		},

		// Unknown fields.
		{
			name: "unknown top-level", md: account, path: "nickname",
			wantErr: []error{projection.ErrInvalidFieldPath, protopath.ErrUnknownField},
		},
		{
			name: "unknown nested", md: account, path: "owner.phone",
			wantErr: []error{projection.ErrInvalidFieldPath, protopath.ErrUnknownField},
		},
		{
			name: "unknown in recursion", md: account, path: "parent.parent.nickname",
			wantErr: []error{projection.ErrInvalidFieldPath, protopath.ErrUnknownField},
		},
		{
			name: "json name without option", md: account, path: "displayName",
			wantErr: []error{projection.ErrInvalidFieldPath, protopath.ErrUnknownField},
		},
		{
			name: "ignore unknown", md: account, path: "owner.phone.code",
			opts: []protopath.Option{protopath.WithIgnoreUnknown(true)},
		},
		{
			name: "ignore unknown keeps grammar", md: account, path: "nickname.0",
			opts:    []protopath.Option{protopath.WithIgnoreUnknown(true)},
			wantErr: []error{projection.ErrUnsupportedPath},
		},
		{
			name: "ignore unknown keeps traversal rules", md: account, path: "members.nickname",
			opts:    []protopath.Option{protopath.WithIgnoreUnknown(true)},
			wantErr: []error{projection.ErrInvalidFieldPath},
		},

		// JSON names.
		{
			name: "json name", md: account, path: "backupOwner.email",
			opts: []protopath.Option{protopath.WithJSONNames(true)},
		},
		{
			name: "json name with proto name", md: account, path: "backup_owner.create_time",
			opts: []protopath.Option{protopath.WithJSONNames(true)},
		},
		{
			name: "json name nested", md: account, path: "owner.createTime",
			opts: []protopath.Option{protopath.WithJSONNames(true)},
		},

		// Grammar.
		{name: "empty path", md: account, path: "", wantErr: []error{projection.ErrInvalidFieldPath}},
		{name: "empty segment", md: account, path: "owner..email", wantErr: []error{projection.ErrInvalidFieldPath}},
		{name: "trailing dot", md: account, path: "owner.", wantErr: []error{projection.ErrInvalidFieldPath}},
		{name: "leading digit", md: account, path: "1owner", wantErr: []error{projection.ErrInvalidFieldPath}},
		{name: "invalid character", md: account, path: "owner-email", wantErr: []error{projection.ErrInvalidFieldPath}},
		{name: "wildcard", md: account, path: "*", wantErr: []error{projection.ErrUnsupportedPath}},
		{name: "numeric on message", md: account, path: "owner.0", wantErr: []error{projection.ErrUnsupportedPath}},

		// Depth.
		{
			name: "depth limit", md: account, path: "parent.parent.parent.parent.parent.parent.parent.parent.name",
			wantErr: []error{projection.ErrMaxFieldPathDepthExceeded},
		},
		{
			name: "raised depth limit", md: account, path: "parent.parent.parent.parent.parent.parent.parent.parent.name",
			opts: []protopath.Option{protopath.WithMaxDepth(9)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := mustValidator(t, tt.md, tt.opts...).Validate(tt.path)
			if len(tt.wantErr) == 0 {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)

			for _, want := range tt.wantErr {
				require.ErrorIs(t, err, want)
			}

			require.Contains(t, err.Error(), tt.path, "the error names the offending path")
		})
	}
}

func TestValidateDeepRecursionIsBounded(t *testing.T) {
	t.Parallel()

	account := (&fieldbehaviortestv1.Account{}).ProtoReflect().Descriptor()
	path := strings.Repeat("parent.", 999) + "name"

	err := mustValidator(t, account).Validate(path)
	require.ErrorIs(t, err, projection.ErrMaxFieldPathDepthExceeded)

	require.NoError(t, mustValidator(t, account, protopath.WithMaxDepth(1000)).Validate(path))
}

func TestValidatePlugsIntoProjection(t *testing.T) {
	t.Parallel()

	v := mustValidator(t, (&fieldbehaviortestv1.Account{}).ProtoReflect().Descriptor())

	parser, err := projection.NewParser(projection.WithPathGrammar(v.Validate))
	require.NoError(t, err)

	_, err = parser.Parse(t.Context(), "display_name,owner.email,members")
	require.NoError(t, err)

	_, err = parser.Parse(t.Context(), "display_name,members.email")
	require.ErrorIs(t, err, projection.ErrInvalidFieldPath)

	_, err = projection.NewPolicy(projection.WithPathValidator(v.Validate), projection.WithDeniedFields("owner.email"))
	require.NoError(t, err)

	_, err = projection.NewPolicy(projection.WithPathValidator(v.Validate), projection.WithDeniedFields("owner.phone"))
	require.ErrorIs(t, err, protopath.ErrUnknownField)
}
