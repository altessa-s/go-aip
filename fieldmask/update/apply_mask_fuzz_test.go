// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// FuzzApplyMask feeds client update masks (paths separated by ",") to
// ApplyMask. On an error the request is left untouched; on success the
// effective mask is a mask a client could send back: every path parses, none
// covers another, and every path resolves against the resource.
func FuzzApplyMask(f *testing.F) {
	for _, seed := range []string{
		"description", "profile,profile.id", "description,description.no_such", "aliases,aliases.0.id", "*", "*,*", ",",
		"labels.`a.b`.display_name", "aliases.*.display_name,labels.k", "profile.displayName,create_time", "password,tenant_id",
	} {
		f.Add(seed, false)
		f.Add(seed, true)
	}

	req := &pb.Resource{
		Id: "r", Name: "n", Description: "d", Password: "p", CreateTime: "c",
		Profile: &pb.Profile{Id: "p", DisplayName: "n", UpdatedAt: "u", Secret: "s"},
		Aliases: []*pb.Profile{{Id: "a", DisplayName: "x", UpdatedAt: "u"}},
		Labels:  map[string]*pb.Profile{"k": {Id: "l", DisplayName: "y"}, "a.b": {DisplayName: "z"}},
	}
	md := req.ProtoReflect().Descriptor()

	f.Fuzz(func(t *testing.T, s string, lenient bool) {
		opts := []update.Option{update.WithCurrent(req)}
		if lenient {
			opts = append(opts, update.WithPathValidation(update.PathValidationDisabled))
		}

		msg := proto.Clone(req)

		got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: strings.Split(s, ",")}, msg, opts...)
		if err != nil {
			require.Nil(t, got)
			require.True(t, proto.Equal(req, msg), "the request changed on %v", err)

			return
		}

		paths := got.ToPaths()
		_, err = fieldmask.ParsePaths(paths...)
		require.NoError(t, err)

		for _, p := range paths {
			require.NotEmpty(t, fieldmask.ResolvePath(md, p), "%q", p)
		}
	})
}
