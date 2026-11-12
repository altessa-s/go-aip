// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"fmt"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func TestPrepareProtectedSelection(t *testing.T) {
	for _, dynamic := range []bool{false, true} {
		for _, entry := range []string{"Complete", "Patch"} {
			for _, protected := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
				for _, tc := range []struct {
					name   string
					paths  []string
					keep   string
					change bool
					refuse bool
				}{
					{name: "unselected", paths: []string{"display_name"}, change: true},
					{name: "empty", paths: []string{}, change: true},
					{name: "output only", paths: []string{"update_time"}, change: true},
					{name: "covering parent", paths: []string{"parent"}, keep: "parent"},
					{name: "changed parent", paths: []string{"parent"}, keep: "parent", change: true, refuse: true},
					{name: "nested mutable", paths: []string{"parent.display_name"}, keep: "nested"},
					{name: "list", paths: []string{"members"}, keep: "members"},
					{name: "changed list", paths: []string{"members"}, keep: "members", change: true, refuse: true},
					{name: "list descendant", paths: []string{"members.*.note"}, keep: "member notes"},
					{name: "map", paths: []string{"delegates"}, keep: "delegates"},
					{name: "changed map", paths: []string{"delegates"}, keep: "delegates", change: true, refuse: true},
				} {
					t.Run(fmt.Sprintf("%t/%s/%s/%s", dynamic, entry, protected, tc.name), func(t *testing.T) {
						convert := func(m proto.Message) proto.Message {
							if !dynamic {
								return proto.Clone(m)
							}

							out := dynamicpb.NewMessage(m.ProtoReflect().Descriptor())
							proto.Merge(out, m)

							return out
						}
						policy := sourcePolicy{identity: "etag", fields: map[protoreflect.FullName]annotations.FieldBehavior{
							"fieldbehaviortest.v1.Account.name":        annotations.FieldBehavior_IDENTIFIER,
							"fieldbehaviortest.v1.Account.region":      annotations.FieldBehavior_IMMUTABLE,
							"fieldbehaviortest.v1.Account.update_time": annotations.FieldBehavior_OUTPUT_ONLY,
							"fieldbehaviortest.v1.Owner.org":           protected,
						}}
						input := &pb.Account{
							Name: "accounts/one", Etag: "revision", DisplayName: "Changed", UpdateTime: "ignored",
							Parent:    &pb.Account{Name: "accounts/parent", Region: "fixed", DisplayName: "Parent"},
							Members:   []*pb.Owner{{Org: "fixed", Note: "note"}},
							Delegates: map[string]*pb.Owner{"key": {Org: "fixed", Note: "note"}},
						}

						stored := convert(input)
						if tc.change {
							input.Parent.Region = "INVALID NAME"
							input.Members[0].Org = "INVALID NAME"
							input.Delegates["key"].Org = "INVALID NAME"
						}

						original, req := convert(input), convert(input)
						before := proto.Clone(stored)

						pending, err := update.Prepare(maskOf(tc.paths), req, policy.options()...)
						if err != nil {
							t.Fatal(err)
						}

						want := &pb.Account{Name: input.Name, Etag: input.Etag}

						switch tc.keep {
						case "parent":
							want.Parent = input.Parent
						case "nested":
							want.Parent = &pb.Account{Name: input.Parent.Name, DisplayName: input.Parent.DisplayName}
						case "member notes":
							want.Members = []*pb.Owner{{Note: "note"}}
							if protected == annotations.FieldBehavior_IDENTIFIER {
								want.Members[0].Org = input.Members[0].Org
							}
						case "members":
							want.Members = input.Members
						case "delegates":
							want.Delegates = input.Delegates
						default:
							if tc.name == "unselected" {
								want.DisplayName = input.DisplayName
							}
						}

						requireProtoEqual(t, convert(want), req)

						if entry == "Complete" {
							_, err = pending.Complete(stored)

							if tc.refuse {
								requireProtoEqual(t, original, req)
							}
						} else {
							spec, specErr := update.NewSpec(req, policy.options()...)
							if specErr != nil {
								t.Fatal(specErr)
							}

							patch := sourcePatch(spec, stored, func(proto.Message, update.Plan) {})

							var out proto.Message

							out, err = patch.Run(update.ContextWithPlan(t.Context(), pending), req, "", pending.Mask().ToProtoFieldMask())
							if err == nil {
								requireProtoEqual(t, before, out)
							}
						}

						if (err != nil) != tc.refuse {
							t.Fatalf("refusal wanted: %t, got %v", tc.refuse, err)
						}

						requireProtoEqual(t, before, stored)
					})
				}
			}
		}
	}
}
