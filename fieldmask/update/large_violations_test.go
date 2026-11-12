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

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func TestLargeDistinctProtectedViolations(t *testing.T) {
	// Exercise ordered diagnostics across every behavior/container/entry point.
	// TestViolationDeduplicationGrowth separately checks 30,000 distinct values
	// and the linear insertion structure without repeating full update setup.
	const count = 128

	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		for _, collection := range []string{"members", "delegates"} {
			t.Run(fmt.Sprintf("%s/%s", b, collection), func(t *testing.T) {
				req := &pb.Account{}
				want := make([]update.FieldViolation, count)

				reason := "immutable field cannot be changed from its stored value"
				if b == annotations.FieldBehavior_IDENTIFIER {
					reason = "identifier field cannot be changed from its stored value"
				}

				for i := range count {
					owner := &pb.Owner{Email: "e", Org: "client"}
					path := fmt.Sprintf("members.%d.org", i)

					if collection == "members" {
						req.Members = append(req.Members, owner)
					} else {
						if req.Delegates == nil {
							req.Delegates = make(map[string]*pb.Owner, count)
						}

						key := fmt.Sprintf("k%05d", i)
						req.Delegates[key] = owner
						path = "delegates." + key + ".org"
					}

					want[i] = update.FieldViolation{Path: path, Behavior: b, Reason: reason}
				}

				before := proto.Clone(req)
				policy := update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
					if fd.Name() == "org" {
						return []annotations.FieldBehavior{b}
					}

					return fieldbehavior.AnnotationResolver{}.Resolve(fd)
				}))

				for _, entry := range []string{"ApplyMask", "Apply", "ValidateStrict", "Complete"} {
					t.Run(entry, func(t *testing.T) {
						msg := proto.Clone(req)
						_, err := round2Entry(t, entry, msg, &pb.Account{}, maskOf([]string{collection}), policy)

						var got []update.FieldViolation
						if be, ok := errors.AsType[*update.BehaviorError](err); ok {
							got = be.Violations
						} else if se, ok := errors.AsType[*update.StrictError](err); ok {
							got = se.Violations
						} else {
							t.Fatalf("missing behavior diagnostics: %v", err)
						}

						if !slices.Equal(got, want) {
							t.Fatalf("diagnostics differ: got %d entries, want all %d fields in order", len(got), count)
						}

						requireProtoEqual(t, before, msg)
					})
				}
			})
		}
	}
}
