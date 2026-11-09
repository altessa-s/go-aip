// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/softdelete"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// book is a stored resource of the example list; DeleteTime is nil unless
// the book is soft-deleted.
type book struct {
	ID         string
	Title      string
	DeleteTime *time.Time
}

func Example_softDeleteRestriction() {
	schema := listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "title", Kind: filter.FieldKindString},
		{Name: "deleteTime", Kind: filter.FieldKindTimestamp, Presence: true}, // nullable: the restriction tests it
	}, []string{"title"}, nil)

	codec, err := pagetoken.New([]byte("an example key of at least 32 bytes!!"))
	if err != nil {
		panic(err)
	}

	books, err := memory.NewListEngine(codec, schema, func(b book) string { return b.ID }, func(b book) map[string]any {
		values := map[string]any{"id": b.ID, "title": b.Title, "deleteTime": nil}
		if b.DeleteTime != nil {
			values["deleteTime"] = *b.DeleteTime
		}

		return values
	})
	if err != nil {
		panic(err)
	}

	deleted := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	stored := []book{{ID: "1", Title: "Dune"}, {ID: "2", Title: "Emma", DeleteTime: &deleted}, {ID: "3", Title: "Ulysses"}, {ID: "4", Title: "Walden"}}
	policy := softdelete.New()

	// ListBooks: the handler sets the restriction from show_deleted.
	list := func(showDeleted bool, token string) ([]string, string, error) {
		req := listing.Request{PageSize: 2, PageToken: token, Restriction: policy.Restriction(showDeleted)}

		p, err := books.List(context.Background(), "/library.v1.Library/ListBooks", req, stored)
		if err != nil {
			return nil, "", err
		}

		titles := make([]string, len(p.Items))
		for i, b := range p.Items {
			titles[i] = b.Title
		}

		return titles, p.NextPageToken, nil
	}

	hidden, next, _ := list(false, "")
	fmt.Println("show_deleted=false:", hidden)

	shown, _, _ := list(true, "")
	fmt.Println("show_deleted=true: ", shown)

	// A token of one show_deleted value is refused with the other.
	_, _, err = list(true, next)
	fe, _ := errors.AsType[*coreerrs.FieldError](err)
	fmt.Println("token reused with show_deleted=true:", fe.Field)
	// Output:
	// show_deleted=false: [Dune Ulysses]
	// show_deleted=true:  [Dune Emma]
	// token reused with show_deleted=true: pageToken
}
