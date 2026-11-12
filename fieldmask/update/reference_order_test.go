// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// Each base-only owner diagnostic must be discarded even when the changed
// sibling forces the reference walk to run. These are intentional differences
// of the merge-effect rule, not exemptions from the reference ordering.
func TestReferenceOrderDropsIgnoredChanges(t *testing.T) {
	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			switch fd.Name() {
			case "owner", "region":
				return []annotations.FieldBehavior{b}
			case "create_time":
				return []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY}
			}

			return nil
		})

		for _, tc := range []struct {
			name, path  string
			req, stored *pb.Owner
		}{
			{"output-only descendant", "owner", &pb.Owner{Email: "same", CreateTime: "client"}, &pb.Owner{Email: "same", CreateTime: "server"}},
			{"output-only selection", "owner.create_time", nil, &pb.Owner{Email: "same", CreateTime: "server"}},
			{"parent presence only", "owner.note", nil, &pb.Owner{Email: "same"}},
		} {
			for _, entry := range []string{"ApplyMask", "Complete"} {
				t.Run(fmt.Sprintf("%s/%s/%s", b, tc.name, entry), func(t *testing.T) {
					req := &pb.Account{Owner: tc.req, Region: "new"}
					stored := &pb.Account{Owner: tc.stored, Region: "old"}
					before := proto.Clone(stored)
					_, err := round2Entry(t, entry, req, stored, maskOf([]string{tc.path, "region"}),
						update.WithIdentityFields(), update.WithResolver(resolver))

					var violation *update.BehaviorError
					if !errors.As(err, &violation) || len(violation.Violations) != 1 ||
						violation.Violations[0].Path != "region" || violation.Violations[0].Behavior != b {
						t.Fatalf("reference reintroduced an ignored change: %v", err)
					}

					if !proto.Equal(stored, before) {
						t.Fatal("reference changed stored state")
					}
				})
			}
		}
	}
}

func TestReferenceOrderDropsUnverifiedProtection(t *testing.T) {
	resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		switch fd.FullName() {
		case "google.protobuf.FileDescriptorProto.name":
			return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
		case "google.protobuf.FileDescriptorProto.package":
			return []annotations.FieldBehavior{annotations.FieldBehavior_REQUIRED}
		}

		return nil
	})
	req := &descriptorpb.FileDescriptorProto{Name: new("input.proto")}
	before := proto.Clone(req)
	pending, err := update.Prepare(maskOf([]string{"name", "package"}), req,
		update.WithIdentityFields(), update.WithResolver(resolver))
	want := []update.FieldViolation{{Path: "package", Behavior: annotations.FieldBehavior_REQUIRED,
		Reason: "required field must be set to a non-empty value"}}

	var violation *update.BehaviorError
	if pending != nil || !errors.As(err, &violation) || !slices.Equal(violation.Violations, want) {
		t.Fatalf("unverified immutable input must not precede REQUIRED: %v", err)
	}

	if !proto.Equal(req, before) {
		t.Fatal("reference changed rejected input")
	}
}
