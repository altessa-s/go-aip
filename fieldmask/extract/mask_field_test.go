// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package extract_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask/extract"
)

func TestUpdateMaskDeclaredNames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		fields   []string
		override string
		want     string
	}{
		{name: "camel default", fields: []string{"updateMask"}, want: "updateMask"},
		{name: "snake default", fields: []string{"update_mask"}, want: "update_mask"},
		{name: "snake wins", fields: []string{"updateMask", "update_mask"}, want: "update_mask"},
		{name: "explicit snake does not fall back", fields: []string{"updateMask"}, override: "update_mask"},
		{name: "explicit camel is not a JSON alias", fields: []string{"update_mask"}, override: "updateMask"},
		{name: "explicit camel", fields: []string{"updateMask"}, override: "updateMask", want: "updateMask"},
		{name: "explicit snake", fields: []string{"update_mask"}, override: "update_mask", want: "update_mask"},
		{name: "override wins", fields: []string{"update_mask", "updateMask"}, override: "updateMask", want: "updateMask"},
		{name: "custom exact name", fields: []string{"mask"}, override: "mask", want: "mask"},
		{name: "custom requires override", fields: []string{"mask"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := newMaskRequest(t, tc.fields...)
			opts := maskFieldOptions(tc.override)
			wantOK := tc.want != ""
			require.Equal(t, wantOK, extract.IsCanonicalUpdateRequest(req, opts...), "unpopulated request shape")

			resource := req.Mutable(req.Descriptor().Fields().ByName("resource")).Message().Interface()
			for _, name := range tc.fields {
				setDynamicMask(req, name, name)
			}

			mask, gotResource, ok := extract.UpdateMask(req, opts...)
			require.Equal(t, wantOK, ok)

			if !wantOK {
				before := proto.Clone(req)
				require.ErrorIs(t, extract.SetUpdateMask(req, &fieldmaskpb.FieldMask{}, opts...), extract.ErrFieldNotSettable)
				require.True(t, proto.Equal(before, req), "failed lookup must not write a different field")

				_, _, writeback, found := extract.DefaultUpdate(opts...)(t.Context(), req)
				require.False(t, found)
				require.Nil(t, writeback)

				return
			}

			require.Equal(t, []string{tc.want}, mask.GetPaths())
			require.Same(t, resource, gotResource)

			cleaned := &fieldmaskpb.FieldMask{Paths: []string{"name"}}
			require.NoError(t, extract.SetUpdateMask(req, cleaned, opts...))
			require.Equal(t, cleaned.GetPaths(), dynamicMaskPaths(req, tc.want))
			cleaned.Paths[0] = "changed_after_writeback"

			require.Equal(t, []string{"name"}, dynamicMaskPaths(req, tc.want), "writeback copies paths")

			mask, gotResource, writeback, ok := extract.DefaultUpdate(opts...)(t.Context(), req)
			require.True(t, ok)
			require.Equal(t, []string{"name"}, mask.GetPaths())
			require.Same(t, resource, gotResource)
			require.NotNil(t, writeback)
			writeback(&fieldmaskpb.FieldMask{Paths: []string{"description"}})
			require.Equal(t, []string{"description"}, dynamicMaskPaths(req, tc.want))

			require.NoError(t, extract.SetUpdateMask(req, nil, opts...))
			require.False(t, req.Has(req.Descriptor().Fields().ByName(protoreflect.Name(tc.want))))
			_, _, ok = extract.UpdateMask(req, opts...)
			require.False(t, ok, "an unset selected field must not fall back")
			require.True(t, extract.IsCanonicalUpdateRequest(req, opts...))

			require.NoError(t, extract.SetUpdateMask(req, &fieldmaskpb.FieldMask{}, opts...))
			mask, _, ok = extract.UpdateMask(req, opts...)
			require.True(t, ok, "an empty but populated mask is present")
			require.Empty(t, mask.GetPaths())

			for _, name := range tc.fields {
				if name != tc.want {
					require.Equal(t, []string{name}, dynamicMaskPaths(req, name), "unselected mask must stay unchanged")
				}
			}
		})
	}
}

func TestReadMaskDeclaredNames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		fields   []string
		override string
		want     string
	}{
		{name: "camel default", fields: []string{"readMask"}, want: "readMask"},
		{name: "snake default", fields: []string{"read_mask"}, want: "read_mask"},
		{name: "snake wins", fields: []string{"readMask", "read_mask"}, want: "read_mask"},
		{name: "explicit snake does not fall back", fields: []string{"readMask"}, override: "read_mask"},
		{name: "explicit camel is not a JSON alias", fields: []string{"read_mask"}, override: "readMask"},
		{name: "explicit camel", fields: []string{"readMask"}, override: "readMask", want: "readMask"},
		{name: "override wins", fields: []string{"read_mask", "readMask"}, override: "readMask", want: "readMask"},
		{name: "custom exact name", fields: []string{"mask"}, override: "mask", want: "mask"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := newMaskRequest(t, tc.fields...)
			for _, name := range tc.fields {
				setDynamicMask(req, name, name)
			}

			opts := maskFieldOptions(tc.override)
			mask, ok := extract.ReadMask(req, opts...)
			require.Equal(t, tc.want != "", ok)

			defaultMask, defaultOK := extract.DefaultRead(opts...)(t.Context(), req)
			require.Equal(t, ok, defaultOK)
			require.True(t, proto.Equal(mask, defaultMask))

			if tc.want != "" {
				require.Equal(t, []string{tc.want}, mask.GetPaths())
				req.Clear(req.Descriptor().Fields().ByName(protoreflect.Name(tc.want)))
				_, ok = extract.ReadMask(req, opts...)
				require.False(t, ok, "an unset selected field must not fall back")
			}
		})
	}
}

func TestMaskFieldTypeAndDeclaredName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		change func(*descriptorpb.FieldDescriptorProto)
	}{
		{
			name: "wrong type",
			change: func(fd *descriptorpb.FieldDescriptorProto) {
				fd.Type = descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
				fd.TypeName = nil
			},
		},
		{
			name: "repeated mask",
			change: func(fd *descriptorpb.FieldDescriptorProto) {
				fd.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
			},
		},
		{
			name: "JSON name alone",
			change: func(fd *descriptorpb.FieldDescriptorProto) {
				fd.JsonName = fd.Name
				fd.Name = new("other_mask")
			},
		},
	} {
		for _, names := range [][]string{{"updateMask"}, {"readMask"}, {"update_mask", "updateMask"}, {"read_mask", "readMask"}} {
			t.Run(tc.name+"/"+names[0], func(t *testing.T) {
				t.Parallel()

				req := newMaskRequest(t, names...)
				file := protodesc.ToFileDescriptorProto(req.Descriptor().ParentFile())
				tc.change(file.MessageType[1].Field[1])
				fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
				require.NoError(t, err)

				req = dynamicpb.NewMessage(fd.Messages().ByName("Request"))
				req.Mutable(req.Descriptor().Fields().ByName("resource"))

				if tc.name == "JSON name alone" {
					setDynamicMask(req, "other_mask", "name")
				}

				if len(names) == 2 {
					setDynamicMask(req, names[1], "name")
				}

				// Renaming the snake_case declaration makes the camelCase fallback eligible.
				wantOK := tc.name == "JSON name alone" && len(names) == 2
				if names[0] == "updateMask" || names[0] == "update_mask" {
					require.Equal(t, wantOK, extract.IsCanonicalUpdateRequest(req))
					_, _, ok := extract.UpdateMask(req)
					require.Equal(t, wantOK, ok)

					err := extract.SetUpdateMask(req, &fieldmaskpb.FieldMask{})
					if wantOK {
						require.NoError(t, err)
					} else {
						require.ErrorIs(t, err, extract.ErrFieldNotSettable)
					}
				} else {
					_, ok := extract.ReadMask(req)
					require.Equal(t, wantOK, ok)
				}
			})
		}
	}
}

func maskFieldOptions(name string) []extract.Option {
	if name == "" {
		return nil
	}

	return []extract.Option{extract.WithMaskField(name)}
}

// newMaskRequest builds actual camelCase declarations, not just JSON aliases.
func newMaskRequest(t *testing.T, names ...string) *dynamicpb.Message {
	t.Helper()

	fields := []*descriptorpb.FieldDescriptorProto{{
		Name:     new("resource"),
		Number:   new(int32(1)),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		TypeName: new(".extracttest.Resource"),
	}}
	for i, name := range names {
		fields = append(fields, &descriptorpb.FieldDescriptorProto{
			Name:     new(name),
			Number:   new(int32(i + 2)),
			Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
			TypeName: new(".google.protobuf.FieldMask"),
		})
	}

	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:       new("extract_mask_field_test.proto"),
		Package:    new("extracttest"),
		Syntax:     new("proto3"),
		Dependency: []string{"google/protobuf/field_mask.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: new("Resource")},
			{Name: new("Request"), Field: fields},
		},
	}, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return dynamicpb.NewMessage(fd.Messages().ByName("Request"))
}

func setDynamicMask(req *dynamicpb.Message, name string, paths ...string) {
	mask := req.Mutable(req.Descriptor().Fields().ByName(protoreflect.Name(name))).Message()
	list := mask.Mutable(mask.Descriptor().Fields().ByName("paths")).List()

	for _, path := range paths {
		list.Append(protoreflect.ValueOfString(path))
	}
}

func dynamicMaskPaths(req *dynamicpb.Message, name string) []string {
	mask := req.Get(req.Descriptor().Fields().ByName(protoreflect.Name(name))).Message()
	list := mask.Get(mask.Descriptor().Fields().ByName("paths")).List()
	paths := make([]string, list.Len())

	for i := range list.Len() {
		paths[i] = list.Get(i).String()
	}

	return paths
}
