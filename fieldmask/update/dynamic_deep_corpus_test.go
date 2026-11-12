// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"fmt"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
)

// These fixtures use only base APIs, so the golden generator can copy them
// unchanged to a8b17ba. Both recursive and acyclic schemas hold mutable trees
// beside annotated root fields; their values exceed the default depth limit.
func deepCorpusDescriptor(t dynamicTesting, recursive bool) protoreflect.MessageDescriptor {
	t.Helper()

	field := func(name string, number int32, typ string) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(number), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()}
		if typ != "" {
			fd.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
			fd.TypeName = new(typ)
		}

		return fd
	}

	root := &descriptorpb.DescriptorProto{Name: new("Resource"), Field: []*descriptorpb.FieldDescriptorProto{
		field("code", 1, ""), field("tree", 2, ".deep.Node0"), field("name", 3, ""), field("output", 4, ""), field("secret", 5, ""),
	}}
	for i, b := range map[int]annotations.FieldBehavior{0: annotations.FieldBehavior_IMMUTABLE, 2: annotations.FieldBehavior_IDENTIFIER, 3: annotations.FieldBehavior_OUTPUT_ONLY, 4: annotations.FieldBehavior_INPUT_ONLY} {
		root.Field[i].Options = &descriptorpb.FieldOptions{}
		proto.SetExtension(root.Field[i].Options, annotations.E_FieldBehavior, []annotations.FieldBehavior{b})
	}

	file := &descriptorpb.FileDescriptorProto{Name: new(fmt.Sprintf("deep_%t.proto", recursive)), Package: new("deep"), Syntax: new("proto3"), MessageType: []*descriptorpb.DescriptorProto{root}}
	for i := range 40 {
		node := &descriptorpb.DescriptorProto{Name: new(fmt.Sprintf("Node%d", i)), Field: []*descriptorpb.FieldDescriptorProto{field("value", 1, "")}}
		if recursive {
			node.Field = append(node.Field, field("child", 2, ".deep.Node0"))
		} else if i < 39 {
			node.Field = append(node.Field, field("child", 2, fmt.Sprintf(".deep.Node%d", i+1)))
		}

		file.MessageType = append(file.MessageType, node)

		if recursive {
			break
		}
	}

	fd, err := protodesc.NewFile(file, nil)
	if err != nil {
		t.Fatal(err)
	}

	return fd.Messages().ByName("Resource")
}

func deepCases(t dynamicTesting) []dynamicCase {
	t.Helper()

	var cases []dynamicCase

	for _, recursive := range []bool{false, true} {
		md := deepCorpusDescriptor(t, recursive)

		for depthIndex, depth := range []int{2, 5, 35} {
			for codeIndex, code := range []string{"", "same"} {
				for _, limit := range []int{0, 1} {
					for mode, paths := range [][]string{nil, {"tree"}, {"tree.child"}, {"tree.child.value"}, {}} {
						// Pair depth, code, limit and selection instead of multiplying all four.
						if limit != (depthIndex+codeIndex+mode)%2 {
							continue
						}

						req := deepCorpusMessage(md, depth, "new", code)
						stored := deepCorpusMessage(md, depth, "old", "same")
						stored.Set(md.Fields().ByName("name"), protoreflect.ValueOfString("resources/1"))

						if code != "" {
							req.Set(md.Fields().ByName("name"), protoreflect.ValueOfString("resources/1"))
						}

						req.Set(md.Fields().ByName("output"), protoreflect.ValueOfString("ignored"))
						req.Set(md.Fields().ByName("secret"), protoreflect.ValueOfString("input"))

						var mask *fieldmaskpb.FieldMask
						if paths != nil {
							mask = &fieldmaskpb.FieldMask{Paths: paths}
						}

						// Descriptor-only fixtures have no generated counterpart: record once.
						cases = append(cases, dynamicCase{Name: fmt.Sprintf("deep%t/depth%d/code%q/limit%d/mask%v/nil%t", recursive, depth, code, limit, paths, paths == nil), Request: req, Stored: stored, Mask: mask, Annotated: true, MaxPathDepth: limit})
					}
				}
			}
		}
	}

	return cases
}

func collisionCases(t dynamicTesting) []dynamicCase {
	t.Helper()

	var cases []dynamicCase

	for keyIndex, key := range []string{"*", "a.b", "x`y", "", "fields", "values", "`*`", "a.*", "\x00*"} {
		path := "`" + strings.ReplaceAll(key, "`", "``") + "`"
		for _, beside := range []bool{false, true} {
			request := &structpb.Struct{Fields: map[string]*structpb.Value{key: structpb.NewStringValue("new")}}

			stored := &structpb.Struct{Fields: map[string]*structpb.Value{key: structpb.NewStringValue("old"), "keep": structpb.NewBoolValue(true)}}
			if beside {
				request.Fields["color"] = structpb.NewStringValue("blue")
			}

			for kind := range 5 {
				var req, cur proto.Message = request, stored

				prefix := ""

				switch kind {
				case 1:
					req, cur = structpb.NewStructValue(request), structpb.NewStructValue(stored)
				case 2:
					req, cur = &pb.UpdateRequest{ExtraData: request}, &pb.UpdateRequest{ExtraData: stored}
					prefix = "extra_data."
				case 3:
					req = &structpb.Struct{Fields: map[string]*structpb.Value{"work": structpb.NewStructValue(request)}}
					cur = &structpb.Struct{Fields: map[string]*structpb.Value{"work": structpb.NewStructValue(stored), "keep": structpb.NewBoolValue(true)}}
					prefix = "work."
				case 4:
					req = &structpb.ListValue{Values: []*structpb.Value{structpb.NewStructValue(request)}}
					cur = &structpb.ListValue{Values: []*structpb.Value{structpb.NewStructValue(stored)}}
					prefix = "*."
				}

				for mode, paths := range [][]string{nil, {}, {"*"}, {prefix + path}, {prefix + "color"}} {
					// Keep every key/container and container/selection pair in both representations.
					if mode != (keyIndex+kind)%5 {
						continue
					}

					var mask *fieldmaskpb.FieldMask
					if paths != nil {
						mask = &fieldmaskpb.FieldMask{Paths: paths}
					}

					for _, dynamic := range []bool{false, true} {
						cases = append(cases, dynamicCase{Name: fmt.Sprintf("collision/%q/beside%t/kind%d/mode%d/dynamic%t", key, beside, kind, mode, dynamic), Request: dynamicForm(t, req, dynamic), Stored: dynamicForm(t, cur, dynamic), Mask: mask})
					}
				}
			}
		}
	}

	return cases
}

func deepCorpusMessage(md protoreflect.MessageDescriptor, depth int, value, code string) *dynamicpb.Message {
	m := dynamicpb.NewMessage(md)
	if code != "" {
		m.Set(md.Fields().ByName("code"), protoreflect.ValueOfString(code))
	}

	node := m.Mutable(md.Fields().ByName("tree")).Message()
	for i := 1; i < depth; i++ {
		node = node.Mutable(node.Descriptor().Fields().ByName("child")).Message()
	}

	node.Set(node.Descriptor().Fields().ByName("value"), protoreflect.ValueOfString(value))

	return m
}
