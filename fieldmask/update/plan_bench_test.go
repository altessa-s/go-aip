// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func BenchmarkMaskSpecPlan(b *testing.B) {
	msg := &pb.Resource{Name: "name"}
	spec, err := update.NewSpec(msg)
	require.NoError(b, err)
	b.ReportAllocs()

	for b.Loop() {
		_, err = spec.Plan(msg, maskOf([]string{"name"}))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResolveMask(b *testing.B) {
	msg := &pb.Resource{}
	spec, err := update.NewSpec(msg, update.WithAllowedPaths("profile", "profile.display_name", "profile.secret", "aliases"))
	require.NoError(b, err)

	mask := maskOf([]string{"profile", "aliases"})

	b.ReportAllocs()

	for b.Loop() {
		_, err = spec.ResolveMask(msg, mask)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValidateResolved(b *testing.B) {
	msg := &pb.Account{Members: []*pb.Owner{{Email: "first"}, {Email: "second"}}}
	spec, err := update.NewSpec(msg)
	require.NoError(b, err)
	resolution, err := spec.ResolveMask(msg, maskOf([]string{"members"}))
	require.NoError(b, err)
	b.ReportAllocs()

	for b.Loop() {
		_, err = spec.ValidateResolved(msg, resolution)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValidateResolvedReadOnly(b *testing.B) {
	msg := &pb.Account{Region: "old", DisplayName: "new", BackupOwner: &pb.Owner{Org: "old"}, Members: make([]*pb.Owner, 256)}

	current := &pb.Account{Region: "old", DisplayName: "old", BackupOwner: msg.BackupOwner, Members: msg.Members}
	for i := range msg.Members {
		msg.Members[i] = &pb.Owner{Email: "member@example.com"}
	}

	spec, err := update.NewSpec(msg)
	require.NoError(b, err)

	for _, tc := range []struct {
		name  string
		paths []string
	}{
		{name: "empty", paths: []string{}},
		{name: "mutable_leaf", paths: []string{"display_name"}},
		{name: "immutable_leaf", paths: []string{"region"}},
		{name: "immutable_nested_leaf", paths: []string{"backup_owner.org"}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			resolved, err := spec.ResolveMask(msg, maskOf(tc.paths))
			require.NoError(b, err)

			withCurrent := update.WithCurrent(current)

			b.ReportAllocs()

			for b.Loop() {
				_, err = spec.ValidateResolved(msg, resolved, withCurrent)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
