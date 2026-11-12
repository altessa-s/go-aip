// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
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

// Add mutable collections of Settings so restoration has to find parents in
// singular children, list elements and map values after Strip replaces them.
func postStripDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	file := protodesc.ToFileDescriptorProto(protectedOutputOnlyDescriptor(t).ParentFile())
	root := file.MessageType[0]
	root.Field = append(root.Field,
		&descriptorpb.FieldDescriptorProto{
			Name: new("settings_list"), Number: new(int32(2)), Label: descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
			Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: new(".protectedoo.Settings"),
		},
		&descriptorpb.FieldDescriptorProto{
			Name: new("settings_map"), Number: new(int32(3)), Label: descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
			Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: new(".protectedoo.Root.SettingsMapEntry"),
		},
		&descriptorpb.FieldDescriptorProto{Name: new("title"), Number: new(int32(4)), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
	)
	root.NestedType = []*descriptorpb.DescriptorProto{{
		Name: new("SettingsMapEntry"), Options: &descriptorpb.MessageOptions{MapEntry: new(true)},
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: new("key"), Number: new(int32(1)), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
			{Name: new("value"), Number: new(int32(2)), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: new(".protectedoo.Settings")},
		},
	}}

	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}

	return fd.Messages().ByName("Root")
}

func TestProtectedRestorationAfterStrip(t *testing.T) {
	md := postStripDescriptor(t)

	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			got := fieldbehavior.AnnotationResolver{}.Resolve(fd)
			if slices.Contains(got, annotations.FieldBehavior_IMMUTABLE) {
				return []annotations.FieldBehavior{b}
			}

			return got
		})
		opts := []update.Option{atomicNames("protectedoo.Settings"), update.WithResolver(resolver)}

		for _, tc := range []struct{ name, path, container string }{
			{"singular", "settings", "settings { %s }"},
			{"repeated", "settings_list", "settings_list { %s } settings_list { %s }"},
			{"map", "settings_map", `settings_map { key: "k" value { %s } } settings_map { key: "a.b" value { %s } }`},
			{"map_key", "settings_map.`a.b`", `settings_map { key: "a.b" value { %s } }`},
		} {
			for _, entry := range []string{"ApplyMask", "Apply", "Complete", "Spec", "BoundSpec", "Patch", "BoundPatch"} {
				t.Run(fmt.Sprintf("%s/%s/%s", b, tc.name, entry), func(t *testing.T) {
					parse := func(stamp, note, title string) *dynamicpb.Message {
						// The second config has only an ignored descendant: its
						// presence must survive stripping so restoration can find it.
						settings := func(stamp, note string) string {
							return fmt.Sprintf(`config { server: %q x: "a" }
							configs { server: %q x: "b" } configs { server: %q }
							named { key: "inner.key" value { server: %q x: "c" } } note: %q`, stamp, stamp, stamp, stamp, note)
						}

						values := []any{settings(stamp, note)}
						if tc.name == "repeated" || tc.name == "map" {
							values = append(values, settings(stamp+"-second", note+"-second"))
						}

						m := dynamicpb.NewMessage(md)

						text := fmt.Sprintf(tc.container, values...) + fmt.Sprintf(" title: %q", title)
						if err := prototext.Unmarshal([]byte(text), m); err != nil {
							t.Fatal(err)
						}

						return m
					}
					stored := parse("saved", "old", "unselected")
					request := parse("client", "new", "discard")
					want := parse("saved", "new", "unselected")
					beforeStored := proto.Clone(stored)
					raw := maskOf([]string{tc.path})
					ctx := t.Context()

					var pending *update.DeferredPlan

					if entry == "Complete" || entry == "BoundSpec" || entry == "BoundPatch" {
						var err error

						pending, err = update.Prepare(raw, request, opts...)
						if err != nil {
							t.Fatal(err)
						}

						ctx = update.ContextWithPlan(ctx, pending)
						if got, ok := update.PlanFromContext(ctx, request); !ok || got != pending {
							t.Fatal("Prepare lost the caller's resource identity")
						}
					}

					spec, err := update.NewSpec(request, opts...)
					if err != nil {
						t.Fatal(err)
					}

					var (
						paths []string
						got   proto.Message
					)

					switch entry {
					case "ApplyMask":
						mask, applyErr := update.ApplyMask(raw, request, append(slices.Clone(opts), update.WithCurrent(stored))...)
						paths, err = mask.ToPaths(), applyErr
					case "Apply":
						mask := fieldmask.FromPaths(tc.path)
						err = update.Apply(mask, request, append(slices.Clone(opts), update.WithCurrent(stored))...)
						paths = mask.ToPaths()
					case "Complete":
						mask, completeErr := pending.Complete(stored)

						paths, err = mask.ToPaths(), completeErr
						if err == nil {
							first := proto.Clone(request)

							mask, err = pending.Complete(stored)
							if !slices.Equal(paths, mask.ToPaths()) || !proto.Equal(first, request) {
								t.Fatal("completion is not idempotent")
							}
						}
					case "Spec", "BoundSpec":
						plan, planErr := spec.Plan(request, raw, update.WithCurrent(stored), update.WithPlanContext(ctx))
						paths, err = plan.Paths, planErr
					case "Patch", "BoundPatch":
						saves := 0
						patch := update.Patch[*dynamicpb.Message, *dynamicpb.Message, *int64]{
							Spec: spec, Precondition: update.Int64Precondition[*dynamicpb.Message]{Version: func(*dynamicpb.Message) int64 { return 1 }},
							Read:    func(context.Context) (*dynamicpb.Message, error) { return stored, nil },
							ToProto: func(m *dynamicpb.Message) *dynamicpb.Message { return m },
							Save: func(_ context.Context, merged *dynamicpb.Message, _ update.Plan, _ *int64) (*dynamicpb.Message, error) {
								saves++

								requireProtoEqual(t, want, merged)

								return merged, nil
							},
						}

						got, err = patch.Run(ctx, request, "", raw)
						if err == nil && saves != 1 {
							t.Fatalf("Save called %d times, want 1", saves)
						}
					}

					if err != nil {
						t.Fatal(err)
					}

					requireProtoEqual(t, beforeStored, stored)

					if got == nil {
						if !slices.Equal(paths, []string{tc.path}) {
							t.Fatalf("mask = %v, want %s", paths, tc.path)
						}

						got = proto.Clone(stored)
						if err := update.MergePaths(got, request, paths, opts...); err != nil {
							t.Fatal(err)
						}
					}

					requireProtoEqual(t, want, got)
				})
			}
		}
	}
}
