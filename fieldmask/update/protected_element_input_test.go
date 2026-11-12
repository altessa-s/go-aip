// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"fmt"
	"slices"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"
)

func protectedElementDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	file := protodesc.ToFileDescriptorProto(protectedOutputOnlyDescriptor(t).ParentFile())
	file.MessageType[0].Field = append(file.MessageType[0].Field, &descriptorpb.FieldDescriptorProto{
		Name: new("title"), Number: new(int32(2)), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
	})
	settings := file.MessageType[1]
	settings.Field[1].Options = nil
	settings.Field[1].TypeName = new(".protectedoo.Element")
	settings.Field[2].Options = nil
	settings.NestedType[0].Field[1].TypeName = new(".protectedoo.Element")
	locked := proto.Clone(settings.Field[0]).(*descriptorpb.FieldDescriptorProto)
	locked.Name, locked.JsonName = new("locked"), new("locked")
	file.MessageType = append(file.MessageType, &descriptorpb.DescriptorProto{Name: new("Element"), Field: []*descriptorpb.FieldDescriptorProto{locked}})

	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}

	return fd.Messages().ByName("Root")
}

func protectedElementMessage(t *testing.T, md protoreflect.MessageDescriptor, locked, title string) proto.Message {
	t.Helper()

	m := dynamicpb.NewMessage(md)

	text := fmt.Sprintf(`title: %q settings { configs { %s } named { key: "k" value { %s } } }`, title, locked, locked)
	if err := prototext.Unmarshal([]byte(text), m); err != nil {
		t.Fatal(err)
	}

	return m
}

func TestProtectedElementIgnoredInput(t *testing.T) {
	md := protectedElementDescriptor(t)

	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		policy := update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "locked" {
				return []annotations.FieldBehavior{b}
			}

			return fieldbehavior.AnnotationResolver{}.Resolve(fd)
		}))

		for _, reverse := range []bool{false, true} {
			for _, entry := range []string{"ApplyMask", "ApplyMaskStrict", "Apply", "ValidateStrict", "Complete", "Spec", "Patch", "DeferredSpec", "DeferredPatch"} {
				t.Run(fmt.Sprintf("%s/reverse=%t/%s", b, reverse, entry), func(t *testing.T) {
					req := protectedElementMessage(t, md, "", "")

					stored := protectedElementMessage(t, md, `locked { x: "e" server: "server" }`, "")
					if reverse {
						req, stored = stored, req
					}

					before := proto.Clone(req)
					paths := []string{"settings.configs.*.locked.server", "settings.named.k.locked.server"}

					mask, err := round2Entry(t, entry, req, stored, maskOf(paths), policy)
					if err != nil || len(mask) != 0 {
						t.Fatalf("want ignored paths, got %v: %v", mask.ToPaths(), err)
					}

					wantInput := before
					if reverse && (entry != "ValidateStrict" && entry != "Spec" && entry != "Patch") {
						wantInput = protectedElementMessage(t, md, `locked { x: "e" }`, "")
					}

					requireProtoEqual(t, wantInput, req)

					merged := proto.Clone(stored)
					if err := update.MergePaths(merged, req, mask.ToPaths()); err != nil {
						t.Fatal(err)
					}

					requireProtoEqual(t, stored, merged)
				})
			}
		}

		for _, atomic := range []bool{false, true} {
			for _, entry := range []string{"ApplyMask", "Apply", "Prepare"} {
				t.Run(fmt.Sprintf("%s/atomic=%t/%s", b, atomic, entry), func(t *testing.T) {
					req := protectedElementMessage(t, md, `locked { x: "e" server: "client" }`, "new")
					stored := protectedElementMessage(t, md, `locked { x: "e" server: "stored" }`, "old")
					paths := []string{"settings.configs", "settings.named", "title"}
					opts := []update.Option{policy}

					if atomic {
						paths = []string{"settings", "title"}

						opts = append(opts, atomicNames("protectedoo.Settings"))
					}

					var pending *update.DeferredPlan

					var mask fieldmask.FieldMask

					var err error

					switch entry {
					case "ApplyMask":
						mask, err = update.ApplyMask(maskOf(paths), req, opts...)
					case "Apply":
						mask = fieldmask.FromPaths(paths...)
						err = update.Apply(mask, req, opts...)
					case "Prepare":
						pending, err = update.Prepare(maskOf(paths), req, opts...)
						if err == nil {
							mask = pending.Mask()
						}
					}

					if err != nil || !slices.Equal(mask.ToPaths(), []string{"title"}) {
						t.Fatalf("want only title, got %v: %v", mask.ToPaths(), err)
					}

					requireProtoEqual(t, protectedElementMessage(t, md, `locked { x: "e" }`, "new"), req)

					if pending != nil {
						mask, err = pending.Complete(stored)
						if err != nil || !slices.Equal(mask.ToPaths(), paths) {
							t.Fatalf("want %v, got %v: %v", paths, mask.ToPaths(), err)
						}

						merged := proto.Clone(stored)
						if err := update.MergePaths(merged, req, mask.ToPaths(), opts...); err != nil {
							t.Fatal(err)
						}

						requireProtoEqual(t, protectedElementMessage(t, md, `locked { x: "e" server: "stored" }`, "new"), merged)
					}
				})
			}
		}
	}
}
