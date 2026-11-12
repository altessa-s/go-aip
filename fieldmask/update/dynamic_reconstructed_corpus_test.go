// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
)

// Keep generated/reconstructed pairs in the same order as the ordinary corpus.
// Both dynamic roots and every nested value share the reconstructed descriptor,
// whose identity differs from the canonical generated struct.proto descriptor.
func reconstructedCases(t dynamicTesting) []dynamicCase {
	t.Helper()

	file, err := protodesc.NewFile(protodesc.ToFileDescriptorProto(structpb.File_google_protobuf_struct_proto), nil)
	if err != nil {
		t.Fatal(err)
	}

	object := func(value string, keep bool) *structpb.Struct {
		leaf := func() *structpb.Struct {
			fields := map[string]*structpb.Value{"x": structpb.NewStringValue(value)}
			if keep {
				fields["keep"] = structpb.NewStringValue("v")
			}

			return &structpb.Struct{Fields: fields}
		}
		out := leaf()
		out.Fields["nested"] = structpb.NewStructValue(leaf())
		out.Fields["items"] = structpb.NewListValue(&structpb.ListValue{Values: []*structpb.Value{structpb.NewStructValue(leaf())}})

		return out
	}
	request, stored := object("new", false), object("old", true)
	requestList := &structpb.ListValue{Values: []*structpb.Value{structpb.NewStructValue(request)}}
	storedList := &structpb.ListValue{Values: []*structpb.Value{structpb.NewStructValue(stored)}}

	var cases []dynamicCase

	for _, root := range []struct {
		name  string
		req   proto.Message
		cur   proto.Message
		paths []string
	}{
		{"Struct", request, stored, []string{"x", "nested.x", "items.*.x", "nested", "items", "*"}},
		{"Value/struct", structpb.NewStructValue(request), structpb.NewStructValue(stored), []string{"x", "nested.x", "items.*.x", "nested", "*"}},
		{"ListValue", requestList, storedList, []string{"*.x", "*.nested.x", "*.items.*.x", "*"}},
		{"Value/list", structpb.NewListValue(requestList), structpb.NewListValue(storedList), []string{"*.x", "*.nested.x", "*.items.*.x", "*"}},
	} {
		for _, path := range root.paths {
			for _, reconstructed := range []bool{false, true} {
				convert := func(m proto.Message) proto.Message {
					if !reconstructed {
						return proto.Clone(m)
					}

					md := file.Messages().ByName(m.ProtoReflect().Descriptor().Name())
					if md == m.ProtoReflect().Descriptor() {
						t.Fatal("fixture must reconstruct descriptor identity")
					}

					out := dynamicpb.NewMessage(md)

					wire, err := proto.Marshal(m)
					if err != nil {
						t.Fatal(err)
					}

					if err := proto.Unmarshal(wire, out); err != nil {
						t.Fatal(err)
					}

					return out
				}
				cases = append(cases, dynamicCase{
					Name:    fmt.Sprintf("reconstructed/%s/%s/dynamic%t", root.name, path, reconstructed),
					Request: convert(root.req), Stored: convert(root.cur), Mask: &fieldmaskpb.FieldMask{Paths: []string{path}},
					Reconstructed: reconstructed,
				})
			}
		}
	}

	return cases
}

func dynamicCaseEntries(c dynamicCase) []string {
	if strings.HasPrefix(c.Name, "reconstructed/") {
		return append(slices.Clone(dynamicEntries), "MergePaths")
	}

	return dynamicEntries
}
