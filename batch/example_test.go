// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package batch_test

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/altessa-s/go-aip/batch"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/batchtest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func ExampleValidateRequest() {
	req := &pb.BatchCreateBooksRequest{
		Parent: "publishers/1",
		Requests: []*pb.CreateBookRequest{
			{Book: &pb.Book{Title: "Dune"}},
			{Parent: "publishers/2", Book: &pb.Book{Title: "Emma"}},
		},
	}

	err := batch.ValidateRequest(req)

	fe, _ := errors.AsType[*coreerrs.FieldError](err)
	fmt.Println(fe.Field)
	fmt.Println(errors.Is(err, batch.ErrParentMismatch))
	// Output:
	// requests[1].parent
	// true
}

func ExampleEach() {
	reqs := []*pb.CreateBookRequest{{Book: &pb.Book{Title: "Dune"}}, {Book: &pb.Book{}}}

	err := batch.Each(batch.FieldRequests, reqs, func(_ int, r *pb.CreateBookRequest) error {
		if r.GetBook().GetTitle() == "" {
			return &coreerrs.FieldError{Field: "book.title", Reason: "is required"}
		}

		return nil
	})

	fmt.Println(err)
	// Output:
	// batch: requests[1].book.title: is required
}

func ExampleGetKeys() {
	stored := map[string]string{"b/1": "Dune", "b/2": "Emma"}
	fetch := func(_ context.Context, ids []string) (map[string]string, error) {
		out := map[string]string{}

		for _, id := range ids {
			if title, ok := stored[id]; ok {
				out[id] = title
			}
		}

		return out, nil
	}

	titles, err := batch.GetKeys(context.Background(), batch.FieldNames, []string{"b/2", "b/1"}, fetch)
	fmt.Println(titles, err)

	_, err = batch.GetKeys(context.Background(), batch.FieldNames, []string{"b/1", "b/3"}, fetch)
	fmt.Println(err, errors.Is(err, coreerrs.ErrNotFound))
	// Output:
	// [Emma Dune] <nil>
	// batch: names[1]: resource not found true
}

func ExampleAtomic() {
	// inTx stands for the unit of work of a store: it commits when fn
	// returns nil and rolls back otherwise.
	inTx := func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

	upper := func(_ context.Context, _ int, title string) (string, error) {
		if title == "" {
			return "", &coreerrs.FieldError{Field: "book.title", Reason: "is required"}
		}

		return strings.ToUpper(title), nil
	}

	titles, err := batch.Atomic(context.Background(), batch.FieldRequests, []string{"dune", "emma"}, inTx, upper)
	fmt.Println(titles, err)

	titles, err = batch.Atomic(context.Background(), batch.FieldRequests, []string{"dune", ""}, inTx, upper)
	fmt.Println(titles, err)
	// Output:
	// [DUNE EMMA] <nil>
	// [] batch: requests[1].book.title: is required
}

func ExamplePartial() {
	del := func(_ context.Context, _ int, id string) (struct{}, error) {
		if id == "b/2" {
			return struct{}{}, coreerrs.ErrNotFound
		}

		return struct{}{}, nil
	}

	res, err := batch.Partial(context.Background(), batch.FieldNames, []string{"b/1", "b/2", "b/3"}, del, batch.WithConcurrency(2))
	fmt.Println(res.SucceededCount(), res.Failed, err)
	// Output:
	// 2 map[1:not found] <nil>
}
