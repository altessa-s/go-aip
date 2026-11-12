// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldbehavior_test

import (
	"fmt"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldbehavior"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func TestStripUpdateProtectedCollections(t *testing.T) {
	t.Parallel()

	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IDENTIFIER} {
		resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "org" {
				return []annotations.FieldBehavior{b}
			}

			return fieldbehavior.AnnotationResolver{}.Resolve(fd)
		})

		for _, known := range []bool{false, true} {
			for _, value := range []string{"old", "new", ""} {
				t.Run(fmt.Sprintf("%s/current=%t/value=%s", b, known, value), func(t *testing.T) {
					stored := &pb.Account{Members: []*pb.Owner{{Org: "old"}}, Delegates: map[string]*pb.Owner{"k": {Org: "old"}}}
					req := &pb.Account{Members: []*pb.Owner{{Org: value, CreateTime: "client"}},
						Delegates: map[string]*pb.Owner{"k": {Org: value, CreateTime: "client"}}}
					before := proto.Clone(req)

					opts := []fieldbehavior.Option{fieldbehavior.WithResolver(resolver), fieldbehavior.WithStrict(true)}
					if known {
						opts = append(opts, fieldbehavior.WithCurrent(stored))
					}

					err := fieldbehavior.StripUpdate(req, opts...)
					if known && value != "old" {
						if err == nil || !proto.Equal(before, req) {
							t.Fatalf("changed protected collection input must fail atomically: %v", err)
						}

						return
					}

					if err != nil {
						t.Fatal(err)
					}

					if req.Members[0].Org != value || req.Delegates["k"].Org != value {
						t.Fatal("protected collection values were discarded")
					}

					if req.Members[0].CreateTime != "" || req.Delegates["k"].CreateTime != "" {
						t.Fatal("output-only input was retained")
					}
				})
			}
		}
	}
}

func TestStripUpdateRemovedProtectedElement(t *testing.T) {
	t.Parallel()

	for _, collection := range []string{"list", "map"} {
		t.Run(collection, func(t *testing.T) {
			stored := &pb.Account{Members: []*pb.Owner{{Org: "keep"}, {Org: "removed"}}}

			req := &pb.Account{Members: []*pb.Owner{{Org: "keep", CreateTime: "client"}}}
			if collection == "map" {
				stored = &pb.Account{Delegates: map[string]*pb.Owner{"keep": {Org: "keep"}, "removed": {Org: "removed"}}}
				req = &pb.Account{Delegates: map[string]*pb.Owner{"keep": {Org: "keep", CreateTime: "client"}}}
			}

			before := proto.Clone(req)
			if err := fieldbehavior.StripUpdate(req, fieldbehavior.WithCurrent(stored)); err == nil {
				t.Fatal("removed protected element was accepted")
			}

			if !proto.Equal(before, req) {
				t.Fatal("failed update changed the request")
			}
		})
	}
}
