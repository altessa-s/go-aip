// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package requestid_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/altessa-s/go-aip/requestid"

	v1 "github.com/altessa-s/go-aip/internal/proto/gen/requestidtest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func Example() {
	guard, err := requestid.New(requestid.NewMemoryStore())
	if err != nil {
		panic(err)
	}

	created := 0
	createBook := func(_ context.Context, req *v1.CreateBookRequest) (*v1.Book, error) {
		created++
		return &v1.Book{Name: req.GetParent() + "/books/" + req.GetBookId(), Title: req.GetBook().GetTitle()}, nil
	}

	// The method and the authenticated caller scope the request ID.
	scope := requestid.Scope{Method: "/library.v1.LibraryService/CreateBook", Principal: "users/alice"}
	req := &v1.CreateBookRequest{
		Parent:    "publishers/p1",
		BookId:    "dune",
		Book:      &v1.Book{Title: "Dune"},
		RequestId: "8d3c6c1e-3b0f-4b8e-9a51-7f3f5d0c2a11",
	}

	first, _ := requestid.Do(context.Background(), guard, scope, req, createBook)
	retry, _ := requestid.Do(context.Background(), guard, scope, req, createBook) // a client retry

	fmt.Println(first.GetName(), retry.GetName(), created)

	// The same request ID with other parameters is rejected.
	req.Book.Title = "Emma"
	_, err = requestid.Do(context.Background(), guard, scope, req, createBook)
	fmt.Println(errors.Is(err, requestid.ErrRequestIDReused), errors.Is(err, coreerrs.ErrAlreadyExists))

	// Output:
	// publishers/p1/books/dune publishers/p1/books/dune 1
	// true true
}

func ExampleCanonical() {
	id, err := requestid.Canonical("8D3C6C1E-3B0F-4B8E-9A51-7F3F5D0C2A11", requestid.FormatUUID4)
	fmt.Println(id, err)

	_, err = requestid.Canonical("not-a-uuid", requestid.FormatUUID)
	fmt.Println(err)

	// Output:
	// 8d3c6c1e-3b0f-4b8e-9a51-7f3f5d0c2a11 <nil>
	// the request ID must be a UUID in the 8-4-4-4-12 hexadecimal form
}
