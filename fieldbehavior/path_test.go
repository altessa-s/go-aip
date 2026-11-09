// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldbehavior_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"

	"github.com/altessa-s/go-aip/fieldbehavior"

	testpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// TestStrip_ViolationPaths pins the text of violation paths, which the walk
// renders only on demand: field names, element indexes and AIP-161 quoted map
// keys joined by dots, in declaration then sorted-key order.
func TestStrip_ViolationPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  *testpb.Resource
		want []string
	}{
		{
			name: "nested singular",
			msg:  &testpb.Resource{Profile: &testpb.Profile{Id: "p"}},
			want: []string{"profile.id"},
		},
		{
			name: "element indexes past single digits",
			msg: &testpb.Resource{Aliases: func() []*testpb.Profile {
				out := make([]*testpb.Profile, 12)
				for i := range out {
					out[i] = &testpb.Profile{}
				}

				out[11].Id = "x"

				return out
			}()},
			want: []string{"aliases.11.id"},
		},
		{
			name: "quoted and sorted map keys",
			msg: &testpb.Resource{Labels: map[string]*testpb.Profile{
				"z":       {Id: "1"},
				"a.b":     {Id: "2"},
				"plain_k": {DisplayName: "kept"},
				"":        {Id: "3"},
			}},
			want: []string{"labels.``.id", "labels.`a.b`.id", "labels.z.id"},
		},
		{
			name: "root and nested in declaration order",
			msg:  &testpb.Resource{Id: "r", Profile: &testpb.Profile{Id: "p"}, Aliases: []*testpb.Profile{{Id: "a"}}},
			want: []string{"id", "profile.id", "aliases.0.id"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			before := proto.Clone(tt.msg)
			err := fieldbehavior.StripCreate(tt.msg, fieldbehavior.WithStrict(true))

			vErr, ok := errors.AsType[*fieldbehavior.BehaviorViolationError](err)
			require.True(t, ok, "error %v", err)

			got := make([]string, 0, len(vErr.Violations))
			for _, v := range vErr.Violations {
				got = append(got, v.Path)
			}

			require.Equal(t, tt.want, got)
			require.True(t, proto.Equal(before, tt.msg), "a rejected message is left untouched")
		})
	}
}

// TestStrip_NothingToClearLeavesMessageEqual pins the skipped apply pass: a
// payload with only kept fields is returned unchanged.
func TestStrip_NothingToClearLeavesMessageEqual(t *testing.T) {
	t.Parallel()

	msg := &testpb.Resource{
		Name:        "n",
		Description: "d",
		Profile:     &testpb.Profile{DisplayName: "dn"},
		Aliases:     []*testpb.Profile{{DisplayName: "a"}},
		Labels:      map[string]*testpb.Profile{"k": {DisplayName: "l"}},
	}
	before := proto.Clone(msg)

	require.NoError(t, fieldbehavior.StripCreate(msg))
	require.True(t, proto.Equal(before, msg))
}
