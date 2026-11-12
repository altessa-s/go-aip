// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package behavior_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/behavior"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func TestSameInputValue(t *testing.T) {
	stored := &pb.Resource{
		Description: "same", Tags: []string{"same"}, ServerMeta: map[string]string{"k": "same"},
		Profile: &pb.Profile{Id: "p", UpdatedAt: "server"},
		Aliases: []*pb.Profile{{Id: "p", UpdatedAt: "server"}},
		Labels:  map[string]*pb.Profile{"k": {Id: "p", UpdatedAt: "server"}},
	}

	for _, name := range []string{"description", "tags", "server_meta", "profile", "aliases", "labels"} {
		t.Run(name, func(t *testing.T) {
			req := proto.Clone(stored).(*pb.Resource)
			req.Profile.UpdatedAt, req.Aliases[0].UpdatedAt, req.Labels["k"].UpdatedAt = "", "", "client"
			r, c := req.ProtoReflect(), stored.ProtoReflect()
			fd := c.Descriptor().Fields().ByName(protoreflect.Name(name))
			before := proto.Clone(req)

			if !behavior.SameInputValue(r, c, fd, behavior.Shared) {
				t.Fatal("ignored output-only data changed equality")
			}

			if !proto.Equal(req, before) {
				t.Fatal("comparison mutated its input")
			}

			req.Description, req.Tags[0], req.ServerMeta["k"] = "changed", "changed", "changed"
			req.Profile.Id, req.Aliases[0].Id, req.Labels["k"].Id = "changed", "changed", "changed"

			if behavior.SameInputValue(r, c, fd, behavior.Shared) {
				t.Fatal("changed input was ignored")
			}

			r.Clear(fd)

			if behavior.SameInputValue(r, c, fd, behavior.Shared) {
				t.Fatal("cleared field was ignored")
			}

			if behavior.SameInputValue(nil, c, fd, behavior.Shared) {
				t.Fatal("absent parent was ignored")
			}

			if !behavior.SameInputValue(nil, r, fd, behavior.Shared) {
				t.Fatal("absent values differ")
			}
		})
	}
}

func TestSameInputValuePreservesUnknownFieldEquality(t *testing.T) {
	stored := &pb.Resource{Profile: &pb.Profile{Id: "same", UpdatedAt: "ignored"}}
	stored.Profile.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 1, 0xa8, 0x06, 2})
	request := &pb.Resource{Profile: &pb.Profile{Id: "same"}}
	request.Profile.ProtoReflect().SetUnknown([]byte{0xa8, 0x06, 2, 0xa0, 0x06, 1})
	field := stored.ProtoReflect().Descriptor().Fields().ByName("profile")
	before := proto.Clone(request)

	if !behavior.SameInputValue(request.ProtoReflect(), stored.ProtoReflect(), field, behavior.Shared) {
		t.Fatal("unknown fields reordered by number changed input equality")
	}

	if !proto.Equal(before, request) {
		t.Fatal("reflection comparison mutated the request")
	}

	request.Profile.ProtoReflect().SetUnknown([]byte{0xa8, 0x06, 3, 0xa0, 0x06, 1})

	if behavior.SameInputValue(request.ProtoReflect(), stored.ProtoReflect(), field, behavior.Shared) {
		t.Fatal("changed unknown field was ignored")
	}
}
