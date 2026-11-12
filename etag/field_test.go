// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package etag_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"

	"github.com/altessa-s/go-aip/etag"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/etagtest/v1"
	coreetag "github.com/altessa-s/go-atlas-core/encoding/etag"
)

func TestGet(t *testing.T) {
	t.Parallel()

	got, err := etag.Get(&pb.DeleteBookRequest{Etag: `W/"3"`})
	require.NoError(t, err)
	require.Equal(t, `W/"3"`, got)

	got, err = etag.Get(&pb.Book{Etag: "abc"})
	require.NoError(t, err)
	require.Equal(t, "abc", got)

	got, err = etag.Get((*pb.Book)(nil))
	require.NoError(t, err)
	require.Empty(t, got)

	_, err = etag.Get(nil)
	require.ErrorIs(t, err, etag.ErrNilMessage)

	for _, m := range []proto.Message{&pb.Shelf{}, &pb.Malformed{}, &pb.Lists{}} {
		_, err = etag.Get(m)
		require.ErrorIs(t, err, etag.ErrNoField, "%T", m)
	}
}

func TestStamp(t *testing.T) {
	t.Parallel()

	b := &pb.Book{Etag: "client"}
	require.NoError(t, etag.Stamp(b, etag.FromRevision[int64](4)))
	require.Equal(t, `W/"4"`, b.GetEtag())

	require.NoError(t, etag.Stamp(b, coreetag.Tag{}))
	require.Empty(t, b.GetEtag(), "the zero tag clears the field")

	require.ErrorIs(t, etag.Stamp(nil, coreetag.Strong("x")), etag.ErrNilMessage)
	require.ErrorIs(t, etag.Stamp((*pb.Book)(nil), coreetag.Strong("x")), etag.ErrNilMessage)
	require.ErrorIs(t, etag.Stamp(&pb.Malformed{}, coreetag.Strong("x")), etag.ErrNoField)
	require.ErrorIs(t, etag.Stamp(&pb.Lists{}, coreetag.Strong("x")), etag.ErrNoField)
}
