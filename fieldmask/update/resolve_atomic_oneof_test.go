// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"slices"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/atomicupdatetest/v1"
)

func TestResolveAtomicOneofMask(t *testing.T) {
	t.Parallel()

	spec, err := update.NewSpec(new(pb.Integration),
		update.WithAllowedPaths("name", "enabled", "smtp", "telegram"),
		update.WithIdentityFields("kind", "etag"),
		update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool {
			switch md.FullName() {
			case "atomicupdatetest.v1.SmtpSettings", "atomicupdatetest.v1.TelegramSettings":
				return true
			default:
				return update.DefaultAtomicMessage(md)
			}
		}))
	if err != nil {
		t.Fatal(err)
	}

	identityOnly := &pb.Integration{Kind: pb.Integration_KIND_SMTP, Etag: "v1", Id: "id", LastCheck: "server"}
	smtp := &pb.Integration{
		Kind: pb.Integration_KIND_SMTP, Etag: "v1", Name: "mail", Enabled: true,
		Settings: &pb.Integration_Smtp{Smtp: &pb.SmtpSettings{Host: "relay", Port: 25}},
	}
	foreign := &pb.Integration{
		Kind: pb.Integration_KIND_SMTP, Etag: "v1",
		Settings: &pb.Integration_Telegram{Telegram: &pb.TelegramSettings{}},
	}
	mask := func(paths ...string) *fieldmaskpb.FieldMask { return &fieldmaskpb.FieldMask{Paths: paths} }

	const implied = update.SynthesizedPathIndex

	for _, tt := range []struct {
		name     string
		resource *pb.Integration
		mask     *fieldmaskpb.FieldMask
		want     []update.ResolvedPath
	}{
		{name: "omitted with populated fields and one branch", resource: smtp, want: []update.ResolvedPath{
			{Path: "name", Index: implied}, {Path: "enabled", Index: implied}, {Path: "smtp", Index: implied},
		}},
		{name: "omitted with only identity and output fields", resource: identityOnly},
		{name: "omitted with a foreign branch", resource: foreign, want: []update.ResolvedPath{{Path: "telegram", Index: implied}}},
		{name: "explicit foreign branch retains original index", resource: smtp, mask: mask("name", "telegram"),
			want: []update.ResolvedPath{{Path: "name", Index: 0}, {Path: "telegram", Index: 1}}},
		{name: "explicit branch absent from body", resource: identityOnly, mask: mask("smtp"),
			want: []update.ResolvedPath{{Path: "smtp", Index: 0}}},
		{name: "explicit empty mask", resource: smtp, mask: mask()},
		{name: "explicit zeros and duplicate paths", resource: identityOnly, mask: mask("enabled", "name", "enabled"),
			want: []update.ResolvedPath{{Path: "name", Index: 1}, {Path: "enabled", Index: 0}}},
		{name: "wildcard with branch", resource: smtp, mask: mask("*")},
		{name: "wildcard without branch", resource: identityOnly, mask: mask("*")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := spec.ResolveMask(tt.resource, tt.mask)
			if err != nil {
				t.Fatal(err)
			}

			got := resolved.Paths()
			if slices.Equal(tt.mask.GetPaths(), []string{"*"}) {
				// The service selects the settings case using kind.
				for _, path := range []string{"name", "enabled"} {
					if !slices.Contains(got, update.ResolvedPath{Path: path, Index: implied}) {
						t.Errorf("wildcard paths = %v, missing synthesized %q", got, path)
					}
				}

				for _, path := range got {
					if path.Path == "kind" || path.Path == "etag" {
						t.Errorf("wildcard selected identity path %q", path.Path)
					}
				}
			} else if !slices.Equal(got, tt.want) {
				t.Errorf("resolved paths = %v, want %v", got, tt.want)
			}
		})
	}

	for _, path := range []string{"smtp.host", "telegram.botToken", "kind", "etag", "id", "lastCheck", "last_check", "Name", "unknown", "*"} {
		t.Run("refuse/"+path, func(t *testing.T) {
			_, err := spec.ResolveMask(smtp, mask("name", path))

			var refusal *update.MaskPathError
			if !errors.As(err, &refusal) || refusal.Index != 1 || refusal.Path != path {
				t.Errorf("ResolveMask(%q) = %v, want MaskPathError for entry 1", path, err)
			}
		})
	}

	t.Run("refuse repeated wildcard", func(t *testing.T) {
		_, err := spec.ResolveMask(smtp, mask("*", "*"))

		var refusal *update.MaskPathError
		if !errors.As(err, &refusal) || refusal.Index != 0 || refusal.Path != "*" {
			t.Errorf("ResolveMask([* *]) = %v, want MaskPathError for entry 0", err)
		}
	})
}
