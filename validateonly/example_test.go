// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package validateonly_test

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/altessa-s/go-aip/etag"
	"github.com/altessa-s/go-aip/validateonly"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/etagtest/v1"
)

// ExampleRun validates a Create without writing: the response is the
// would-be resource without its server-generated values.
func ExampleRun() {
	created := 0
	req := &pb.CreateBookRequest{Book: &pb.Book{Title: "Dune", UpdateTime: "client"}, ValidateOnly: true}

	out, err := validateonly.Run(context.Background(), req.GetValidateOnly(),
		func(context.Context) (*pb.Book, error) {
			book := proto.CloneOf(req.GetBook())
			book.Name = "shelves/1/books/generated"
			book.UpdateTime = "2026-01-01T00:00:00Z"

			// AIP-163: omit what only the write can produce.
			if err := validateonly.OmitGenerated(book); err != nil {
				return nil, err
			}

			book.Name = ""

			return book, nil
		},
		func(_ context.Context, book *pb.Book) (*pb.Book, error) {
			created++
			return book, nil
		})

	fmt.Println(out.GetTitle(), out.GetName() == "", out.GetUpdateTime() == "", created, err)
	// Output: Dune true true 0 <nil>
}

// ExampleRun_delete validates a Delete, its etag precondition included.
func ExampleRun_delete() {
	current := etag.FromRevision[int64](3)
	req := &pb.DeleteBookRequest{Name: "shelves/1/books/2", Etag: `W/"2"`, ValidateOnly: true}

	validateOnly, err := validateonly.Requested(req)
	if err != nil {
		panic(err)
	}

	_, err = validateonly.Run(context.Background(), validateOnly,
		func(context.Context) (*emptypb.Empty, error) {
			if checkErr := etag.NewPrecondition().Check(req.GetEtag(), current); checkErr != nil {
				return nil, checkErr
			}

			return &emptypb.Empty{}, nil
		},
		func(_ context.Context, out *emptypb.Empty) (*emptypb.Empty, error) {
			return out, nil // delete here
		})

	fmt.Println(err)
	// Output: etag: version conflict
}
