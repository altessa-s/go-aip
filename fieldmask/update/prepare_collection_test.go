// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func TestPreparePartialCollections(t *testing.T) {
	for _, dynamic := range []bool{false, true} {
		for _, tc := range []struct {
			name   string
			paths  []string
			want   *pb.Account
			refuse bool
		}{
			{name: "key", paths: []string{"delegates.k.note"}, want: &pb.Account{Delegates: map[string]*pb.Owner{"k": {Note: "Changed"}}}},
			{name: "quoted key", paths: []string{"delegates.`a.b`.note"}, want: &pb.Account{Delegates: map[string]*pb.Owner{"a.b": {Note: "Changed"}}}},
			{name: "map wildcard", paths: []string{"delegates.*.note"}, want: &pb.Account{Delegates: map[string]*pb.Owner{"k": {Note: "Changed"}, "a.b": {Note: "Changed"}, "j": {}}}},
			{name: "list wildcard", paths: []string{"members.*.note"}, want: &pb.Account{Members: []*pb.Owner{{}, {Note: "Changed"}, {}}}},
			{name: "whole element", paths: []string{"delegates.k"}, want: &pb.Account{Delegates: map[string]*pb.Owner{"k": {Email: "e", Org: "INVALID NAME", Note: "Changed"}}}, refuse: true},
			{name: "selected immutable ordering", paths: []string{"delegates.k.org", "members.*.org"}, want: &pb.Account{Delegates: map[string]*pb.Owner{"k": {Org: "INVALID NAME"}}, Members: []*pb.Owner{{Org: "INVALID NAME"}, {Org: "INVALID NAME"}, {Org: "INVALID NAME"}}}, refuse: true},
		} {
			t.Run(tc.name+map[bool]string{false: "/generated", true: "/dynamic"}[dynamic], func(t *testing.T) {
				convert := func(m proto.Message) proto.Message {
					if !dynamic {
						return proto.Clone(m)
					}

					out := dynamicpb.NewMessage(m.ProtoReflect().Descriptor())
					proto.Merge(out, m)

					return out
				}
				input := &pb.Account{Name: "accounts/one", Members: []*pb.Owner{{Org: "INVALID NAME"}, {Org: "INVALID NAME", Note: "Changed"}, {Org: "INVALID NAME"}}, Delegates: map[string]*pb.Owner{
					"k": {Email: "e", Org: "INVALID NAME", Note: "Changed"}, "a.b": {Org: "INVALID NAME", Note: "Changed"}, "j": {Org: "INVALID NAME"},
				}}

				stored := proto.CloneOf(input)
				for _, m := range stored.Members {
					m.Org = "fixed"
					m.Note = "old"
				}

				for _, m := range stored.Delegates {
					m.Org = "fixed"
					m.Note = "old"
				}

				raw := maskOf(tc.paths)

				var opts []update.Option

				for _, entry := range []string{"Complete", "Spec", "Patch"} {
					t.Run(entry, func(t *testing.T) {
						req := convert(input)

						pending, err := update.Prepare(raw, req, opts...)
						if err != nil {
							t.Fatal(err)
						}

						want := proto.CloneOf(tc.want)
						want.Name = input.Name
						requireProtoEqual(t, convert(want), req)
						// Even handler mutations must not replace the captured comparison/write input.
						proto.Reset(req)

						baseline := convert(input)

						var wantErr error

						switch entry {
						case "Complete":
							_, wantErr = update.ApplyMask(raw, baseline, append(opts, update.WithCurrent(stored))...)
							_, err = pending.Complete(stored)
							compareDeferredError(t, wantErr, err)
							requireProtoEqual(t, baseline, req)
						case "Spec":
							spec, specErr := update.NewSpec(req, opts...)
							if specErr != nil {
								t.Fatal(specErr)
							}

							expected, expectedErr := spec.Plan(baseline, raw, update.WithCurrent(stored))
							actual, actualErr := spec.Plan(req, pending.Mask().ToProtoFieldMask(), update.WithCurrent(stored), update.WithDeferredPlan(pending))
							wantErr, err = expectedErr, actualErr
							compareDeferredError(t, wantErr, err)

							if err == nil {
								a, b := proto.Clone(stored), proto.Clone(stored)
								if mergeErr := update.MergePaths(a, baseline, expected.Paths, opts...); mergeErr != nil {
									t.Fatal(mergeErr)
								}

								if mergeErr := update.MergePaths(b, req, actual.Paths, opts...); mergeErr != nil {
									t.Fatal(mergeErr)
								}

								requireProtoEqual(t, a, b)
							}
						case "Patch":
							spec, specErr := update.NewSpec(req, opts...)
							if specErr != nil {
								t.Fatal(specErr)
							}

							patch := sourcePatch(spec, stored, func(proto.Message, update.Plan) {})
							expected, expectedErr := patch.Run(t.Context(), baseline, "", raw)
							actual, actualErr := patch.Run(update.ContextWithPlan(t.Context(), pending), req, "", pending.Mask().ToProtoFieldMask())
							wantErr, err = expectedErr, actualErr
							compareDeferredError(t, wantErr, err)

							if err == nil {
								requireProtoEqual(t, expected, actual)
							}
						}

						if (err != nil) != tc.refuse {
							t.Fatalf("refusal wanted %t, got %v", tc.refuse, err)
						}
					})
				}
			})
		}
	}
}

func TestPrepareNestedCollectionPositions(t *testing.T) {
	input := &descriptorpb.FileDescriptorProto{MessageType: []*descriptorpb.DescriptorProto{
		{Name: new("ignored"), NestedType: []*descriptorpb.DescriptorProto{{Name: new("ignored")}}},
		{Name: new("ignored"), NestedType: []*descriptorpb.DescriptorProto{{Name: new("ignored"), Field: []*descriptorpb.FieldDescriptorProto{{Name: new("selected")}}}}},
	}}
	policy := sourcePolicy{fields: map[protoreflect.FullName]annotations.FieldBehavior{
		"google.protobuf.DescriptorProto.name": annotations.FieldBehavior_IMMUTABLE,
	}}
	req := proto.CloneOf(input)

	_, err := update.Prepare(maskOf([]string{"message_type.*.nested_type.*.field"}), req, append(policy.options(), update.WithAtomicMessage(nil))...)
	if err != nil {
		t.Fatal(err)
	}

	want := &descriptorpb.FileDescriptorProto{MessageType: []*descriptorpb.DescriptorProto{
		{NestedType: []*descriptorpb.DescriptorProto{{}}},
		{NestedType: []*descriptorpb.DescriptorProto{{Field: input.MessageType[1].NestedType[0].Field}}},
	}}
	requireProtoEqual(t, want, req)
}
