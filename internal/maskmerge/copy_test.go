// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package maskmerge

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestReplaceMessageConversionBeforeMutation(t *testing.T) {
	file, err := protodesc.NewFile(protodesc.ToFileDescriptorProto(structpb.File_google_protobuf_struct_proto), nil)
	if err != nil {
		t.Fatal(err)
	}

	dst := dynamicpb.NewMessage(file.Messages().ByName("Struct"))
	want := &structpb.Struct{Fields: map[string]*structpb.Value{"keep": structpb.NewStringValue("v")}}

	wire, err := proto.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}

	if err := proto.Unmarshal(wire, dst); err != nil {
		t.Fatal(err)
	}

	before := proto.Clone(dst)

	src := &structpb.Struct{Fields: map[string]*structpb.Value{"bad": structpb.NewStringValue(string([]byte{0xff}))}}
	if err := ReplaceMessage(dst, src.ProtoReflect()); err == nil {
		t.Fatal("invalid UTF-8 must refuse wire conversion")
	}

	if !proto.Equal(before, dst) {
		t.Fatalf("conversion failure changed destination: got %v; want %v", dst, before)
	}

	if err := ReplaceMessage(dst, dst); err != nil {
		t.Fatal(err)
	}

	if !proto.Equal(before, dst) {
		t.Fatal("replacement lost an aliased source")
	}
}

func TestSetValueConvertsReconstructedChildren(t *testing.T) {
	file, err := protodesc.NewFile(protodesc.ToFileDescriptorProto(structpb.File_google_protobuf_struct_proto), nil)
	if err != nil {
		t.Fatal(err)
	}

	source := structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{
		"items": structpb.NewListValue(&structpb.ListValue{Values: []*structpb.Value{structpb.NewStringValue("new")}}),
	}})
	md := source.ProtoReflect().Descriptor()
	rebuilt := dynamicpb.NewMessage(file.Messages().ByName("Value"))

	field := rebuilt.Descriptor().Fields().ByName("struct_value")
	if err := SetValue(rebuilt, field, source.ProtoReflect().Get(md.Fields().ByName("struct_value"))); err != nil {
		t.Fatal(err)
	}

	// dynamicpb accepts this child by full name, despite its different
	// descriptor identity. Equal roots must still convert their children.
	mixed := dynamicpb.NewMessage(rebuilt.Descriptor())
	mixed.Set(field, source.ProtoReflect().Get(md.Fields().ByName("struct_value")))

	if err := ReplaceMessage(rebuilt, mixed); err != nil {
		t.Fatal(err)
	}

	result := &structpb.Value{}
	if err := SetValue(result.ProtoReflect(), md.Fields().ByName("struct_value"), rebuilt.Get(field)); err != nil {
		t.Fatal(err)
	}

	if !proto.Equal(source, result) {
		t.Fatalf("nested conversion lost values: got %v; want %v", result, source)
	}

	source.GetStructValue().Fields["items"].GetListValue().Values[0] = structpb.NewStringValue("changed source")

	proto.Reset(rebuilt.Get(field).Message().Interface())

	if result.GetStructValue().GetFields()["items"].GetListValue().GetValues()[0].GetStringValue() != "new" {
		t.Fatal("converted result borrows source storage")
	}
}

func TestSetValueConversionFailureLeavesFieldUnchanged(t *testing.T) {
	file, err := protodesc.NewFile(protodesc.ToFileDescriptorProto(structpb.File_google_protobuf_struct_proto), nil)
	if err != nil {
		t.Fatal(err)
	}

	deep := structpb.NewStringValue("bottom")
	for range 10001 {
		deep = structpb.NewListValue(&structpb.ListValue{Values: []*structpb.Value{deep}})
	}

	for _, source := range []proto.Message{
		structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{"keep": deep}}),
		&structpb.Struct{Fields: map[string]*structpb.Value{"first": structpb.NewStringValue("new"), "keep": deep}},
		&structpb.ListValue{Values: []*structpb.Value{structpb.NewStringValue("new"), deep}},
	} {
		t.Run(string(source.ProtoReflect().Descriptor().Name()), func(t *testing.T) {
			md := file.Messages().ByName(source.ProtoReflect().Descriptor().Name())
			dst := dynamicpb.NewMessage(md)
			fieldName := map[protoreflect.Name]protoreflect.Name{"Value": "struct_value", "Struct": "fields", "ListValue": "values"}[md.Name()]
			fd := md.Fields().ByName(fieldName)

			dst.SetUnknown([]byte{0xa0, 0x06, 0x01})
			before := proto.Clone(dst)
			value := source.ProtoReflect().Get(source.ProtoReflect().Descriptor().Fields().ByName(fieldName))

			if err := SetValue(dst, fd, value); err == nil || !strings.Contains(err.Error(), "recursion") {
				t.Fatalf("expected a returned wire recursion error, got %v", err)
			}

			if !proto.Equal(before, dst) {
				t.Fatal("failed field conversion published partial content")
			}
		})
	}
}
