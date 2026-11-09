// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package batch_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/batch"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/batchtest/v1"
)

func book(name string) *pb.Book { return &pb.Book{Name: name} }

func TestValidateRequest(t *testing.T) {
	t.Parallel()

	mask := func(paths ...string) *fieldmaskpb.FieldMask { return &fieldmaskpb.FieldMask{Paths: paths} }

	tests := []struct {
		name  string
		req   proto.Message
		opts  []batch.Option
		field string // empty: valid
		cause error
	}{
		// Batch Get, names form.
		{name: "get names", req: &pb.BatchGetBooksRequest{Parent: "publishers/1", Names: []string{"publishers/1/books/1", "publishers/1/books/2"}}},
		{name: "get empty", req: &pb.BatchGetBooksRequest{}, field: "names", cause: batch.ErrEmpty},
		{name: "get empty allowed", req: &pb.BatchGetBooksRequest{}, opts: []batch.Option{batch.WithAllowEmpty(true)}},
		{name: "get too many", req: &pb.BatchGetBooksRequest{Names: []string{"a", "b", "c"}}, opts: []batch.Option{batch.WithMaxItems(2)},
			field: "names", cause: batch.ErrTooManyItems},
		{name: "get parent mismatch", req: &pb.BatchGetBooksRequest{Parent: "publishers/1", Names: []string{"publishers/1/books/1", "publishers/2/books/2"}},
			field: "names[1]", cause: batch.ErrParentMismatch},
		{name: "get wildcard parent", req: &pb.BatchGetBooksRequest{Parent: "publishers/-", Names: []string{"publishers/1/books/1", "publishers/2/books/2"}}},
		{name: "get ids have no parent", req: &pb.BatchGetBooksRequest{Parent: "publishers/1", Names: []string{"0190a", "books/2"}}},
		{name: "get duplicates allowed", req: &pb.BatchGetBooksRequest{Names: []string{"a", "a"}}},
		{name: "get duplicates rejected", req: &pb.BatchGetBooksRequest{Names: []string{"a", "b", "a"}}, opts: []batch.Option{batch.WithUniqueKeys(true)},
			field: "names[2]", cause: batch.ErrDuplicate},
		{name: "get unique", req: &pb.BatchGetBooksRequest{Names: []string{"a", "b"}}, opts: []batch.Option{batch.WithUniqueKeys(true)}},

		// Batch Get, nested form.
		{name: "get nested parent mismatch", req: &pb.BatchGetBooksNestedRequest{Parent: "publishers/1",
			Requests: []*pb.GetBookRequest{{Name: "publishers/2/books/1"}}}, field: "requests[0].name", cause: batch.ErrParentMismatch},

		// Batch Create.
		{name: "create", req: &pb.BatchCreateBooksRequest{Parent: "publishers/1",
			Requests: []*pb.CreateBookRequest{{Parent: "publishers/1"}, {}}}},
		{name: "create parent mismatch", req: &pb.BatchCreateBooksRequest{Parent: "publishers/1",
			Requests: []*pb.CreateBookRequest{{}, {Parent: "publishers/2"}}}, field: "requests[1].parent", cause: batch.ErrParentMismatch},
		{name: "create no batch parent", req: &pb.BatchCreateBooksRequest{
			Requests: []*pb.CreateBookRequest{{Parent: "publishers/1"}, {Parent: "publishers/2"}}}},
		{name: "create hoisted validate_only", req: &pb.BatchCreateBooksRequest{ValidateOnly: true,
			Requests: []*pb.CreateBookRequest{{ValidateOnly: true}, {}}}},
		{name: "create empty", req: &pb.BatchCreateBooksRequest{Parent: "publishers/1"}, field: "requests", cause: batch.ErrEmpty},

		// Batch Update.
		{name: "update", req: &pb.BatchUpdateBooksRequest{Parent: "publishers/1", UpdateMask: mask("title"),
			Requests: []*pb.UpdateBookRequest{{Book: book("publishers/1/books/1"), UpdateMask: mask("title")}, {Book: book("publishers/1/books/2")}}}},
		{name: "update parent mismatch", req: &pb.BatchUpdateBooksRequest{Parent: "publishers/1",
			Requests: []*pb.UpdateBookRequest{{Book: book("publishers/1/books/1")}, {Book: book("publishers/9/books/2")}}},
			field: "requests[1].book.name", cause: batch.ErrParentMismatch},
		{name: "update without resource", req: &pb.BatchUpdateBooksRequest{Parent: "publishers/1",
			Requests: []*pb.UpdateBookRequest{{}}}},
		{name: "update hoisted mask mismatch", req: &pb.BatchUpdateBooksRequest{UpdateMask: mask("title"),
			Requests: []*pb.UpdateBookRequest{{UpdateMask: mask("title")}, {UpdateMask: mask("author")}}},
			field: "requests[1].updateMask", cause: batch.ErrHoistedMismatch},
		{name: "update other kind not hoisted", req: &pb.BatchUpdateBooksRequest{AllowMissing: "x",
			Requests: []*pb.UpdateBookRequest{{AllowMissing: true}}}},
		{name: "update duplicates", req: &pb.BatchUpdateBooksRequest{
			Requests: []*pb.UpdateBookRequest{{Book: book("b/1")}, {Book: book("b/1")}}}, opts: []batch.Option{batch.WithUniqueKeys(true)},
			field: "requests[1].book.name", cause: batch.ErrDuplicate},
		{name: "update by nested resource", req: &pb.BatchUpdateBooksByIDRequest{Parent: "publishers/1",
			Requests: []*pb.UpdateBookByIDRequest{{Ref: &pb.Ref{Id: "1"}, Book: book("publishers/2/books/1")}}},
			field: "requests[0].book.name", cause: batch.ErrParentMismatch},

		// Batch Delete.
		{name: "delete duplicates", req: &pb.BatchDeleteBooksRequest{
			Requests: []*pb.DeleteBookRequest{{Name: "a"}, {Name: "b"}, {Name: "b"}}}, opts: []batch.Option{batch.WithUniqueKeys(true)},
			field: "requests[2].name", cause: batch.ErrDuplicate},
		{name: "delete distinct etags not hoisted", req: &pb.BatchDeleteBooksRequest{
			Requests: []*pb.DeleteBookRequest{{Name: "a", Etag: "1"}, {Name: "b", Etag: "2"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := batch.ValidateRequest(tt.req, tt.opts...)
			if tt.field == "" {
				require.NoError(t, err)
				return
			}

			requireFieldError(t, err, tt.field, tt.cause)
		})
	}
}

func TestValidateRequestNotBatch(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, batch.ValidateRequest(nil), batch.ErrNotBatchRequest)
	require.ErrorIs(t, batch.ValidateRequest(&pb.NotBatchRequest{}), batch.ErrNotBatchRequest)
	require.ErrorIs(t, batch.ValidateRequest(&pb.Book{}), batch.ErrNotBatchRequest)
}

func TestValidateRequestSizeFirst(t *testing.T) {
	t.Parallel()

	req := &pb.BatchCreateBooksRequest{Parent: "publishers/1",
		Requests: []*pb.CreateBookRequest{{Parent: "publishers/2"}, {}, {}}}

	requireFieldError(t, batch.ValidateRequest(req, batch.WithMaxItems(2)), "requests", batch.ErrTooManyItems)
}
