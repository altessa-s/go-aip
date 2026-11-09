// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldmask_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/fieldmask"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// These tests pin the collection branches of the message path builder behind
// FromMessage and FromSetFields that the rest of the suite does not reach.

func TestPinFromMessageCollections(t *testing.T) {
	t.Parallel()

	msg := &pb.Resource{
		Aliases:    []*pb.Profile{{DisplayName: "a"}},
		Labels:     map[string]*pb.Profile{"k": {Id: "p"}},
		Tags:       []string{"t", ""},
		ServerMeta: map[string]string{"m": ""},
	}
	got := fieldmask.FromMessage[[]string](msg)
	require.ElementsMatch(t, []string{
		"aliases",
		"labels", "labels.k", "labels.k.id",
		"tags",
		"server_meta", "server_meta.m",
	}, got, "repeated fields are leaves: no index paths")
}

func TestPinFromMessageNilMessage(t *testing.T) {
	t.Parallel()

	require.Empty(t, fieldmask.FromMessage[[]string]((*pb.Resource)(nil)))
}

func TestPinFromSetFieldsCollections(t *testing.T) {
	t.Parallel()

	msg := &pb.Resource{
		Aliases:    []*pb.Profile{{DisplayName: "a"}, nil},
		Labels:     map[string]*pb.Profile{"k": {}, "nil": nil},
		Tags:       []string{"t", ""},
		ServerMeta: map[string]string{"m": ""},
	}
	got := fieldmask.FromSetFields[[]string](msg)
	require.ElementsMatch(t, []string{
		"aliases",
		"labels", "labels.k",
		"tags",
		"server_meta", "server_meta.m",
	}, got, "nil map values are not set; repeated fields are leaves")
}
