// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package etag_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"

	"github.com/altessa-s/go-aip/etag"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/etagtest/v1"
	coreetag "github.com/altessa-s/go-atlas-core/encoding/etag"
)

func book() *pb.Book {
	return &pb.Book{
		Name: "shelves/1/books/2", Title: "Dune", Etag: `"stale"`, UpdateTime: "2026-01-01T00:00:00Z",
		Shelf: &pb.Shelf{Theme: "sf", Audit: "a1"}, Tags: []string{"x", "y"},
		Labels: map[string]string{"a": "1", "b": "2", "c": "3", "d": "4"},
	}
}

func hash(t *testing.T, m proto.Message, opts ...etag.HashOption) coreetag.Tag {
	t.Helper()

	tag, err := etag.FromMessage(m, opts...)
	require.NoError(t, err)

	return tag
}

func TestFromMessage(t *testing.T) {
	t.Parallel()

	b := book()
	tag := hash(t, b)
	require.False(t, tag.IsWeak())
	require.Len(t, tag.Value(), 43, "unpadded base64url of SHA-256")
	require.True(t, proto.Equal(book(), b), "the message is not modified")

	for range 20 {
		require.Equal(t, tag, hash(t, book()), "deterministic, map order included")
	}

	stamped := book()
	require.NoError(t, etag.Stamp(stamped, tag))
	require.Equal(t, tag, hash(t, stamped), "the etag field is not hashed")

	changed := book()
	changed.Title = "Dune Messiah"
	require.NotEqual(t, tag, hash(t, changed))

	hexTag := hash(t, b, etag.WithHexEncoding(true))
	require.Len(t, hexTag.Value(), 64)

	weak := hash(t, b, etag.WithWeak(true))
	require.True(t, weak.IsWeak())
	require.Equal(t, tag.Value(), weak.Value())

	parsed, err := etag.Parse(tag.String(), etag.WithStrictQuotes(true))
	require.NoError(t, err)
	require.Equal(t, tag, parsed, "a content tag is a valid entity tag")
}

func TestFromMessageExclusions(t *testing.T) {
	t.Parallel()

	touched := book()
	touched.UpdateTime = "2026-02-02T00:00:00Z"
	touched.Shelf.Audit = "a2"

	require.NotEqual(t, hash(t, book()), hash(t, touched))

	byBehavior := etag.WithExcludeBehaviors(annotations.FieldBehavior_OUTPUT_ONLY)
	require.Equal(t, hash(t, book(), byBehavior), hash(t, touched, byBehavior), "OUTPUT_ONLY at any depth")

	byPath := etag.WithExcludeFields("update_time", "shelf.audit")
	require.Equal(t, hash(t, book(), byPath), hash(t, touched, byPath))

	noShelf := book()
	noShelf.Shelf = nil
	require.NotEqual(t, hash(t, book(), byPath), hash(t, noShelf, byPath))
	require.NotPanics(t, func() { hash(t, noShelf, byPath) }, "an unset intermediate message")

	retitled := book()
	retitled.Title = "other"
	require.NotEqual(t, hash(t, book(), byBehavior), hash(t, retitled, byBehavior))

	b := book()
	_ = hash(t, b, byBehavior, byPath)
	require.True(t, proto.Equal(book(), b), "exclusions work on a copy")
}

func TestFromMessageSubset(t *testing.T) {
	t.Parallel()

	subset := etag.WithHashFields("title", "shelf.theme", "tags")

	other := book()
	other.Name, other.UpdateTime, other.Shelf.Audit, other.Labels = "x", "y", "z", nil
	require.Equal(t, hash(t, book(), subset), hash(t, other, subset))

	other.Shelf.Theme = "fantasy"
	require.NotEqual(t, hash(t, book(), subset), hash(t, other, subset))

	empty := &pb.Book{Title: "Dune"}
	require.Equal(t, hash(t, &pb.Book{Title: "Dune"}), hash(t, empty, subset), "unset paths copy nothing")

	b := book()
	_ = hash(t, b, etag.WithHashFields("shelf"), etag.WithExcludeFields("shelf.audit"))
	require.True(t, proto.Equal(book(), b), "excluding below an included message does not touch the source")

	require.Equal(t, hash(t, &pb.Book{}), hash(t, book(), etag.WithHashFields("etag")), "etag is excluded even when included")
}

func TestFromMessageErrors(t *testing.T) {
	t.Parallel()

	_, err := etag.FromMessage(nil)
	require.ErrorIs(t, err, etag.ErrNilMessage)

	_, err = etag.FromMessage((*pb.Book)(nil))
	require.ErrorIs(t, err, etag.ErrNilMessage)

	for _, opt := range []etag.HashOption{
		etag.WithHashFields("nope"),
		etag.WithExcludeFields("shelf.nope"),
		etag.WithExcludeFields("tags.x"),
		etag.WithExcludeFields("title.x"),
		etag.WithHashFields(""),
	} {
		_, err = etag.FromMessage(book(), opt)
		require.ErrorIs(t, err, etag.ErrNoField)
	}
}
