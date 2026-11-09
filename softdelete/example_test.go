// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package softdelete_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/altessa-s/go-atlas-core/time/clock"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/softdelete"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
	sdtestv1 "github.com/altessa-s/go-aip/internal/proto/gen/softdeletetest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func ExamplePolicy() {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	policy := softdelete.New(softdelete.WithRetention(7*24*time.Hour), softdelete.WithClock(clock.NewFake(now)))

	stored := &sdtestv1.Book{Name: "books/1"}

	// DeleteBook: stamp the stored resource, then save it.
	if _, err := policy.Delete(stored); err != nil {
		panic(err)
	}

	fmt.Println("deleted:", stored.GetDeleteTime().AsTime().Format(time.DateOnly))
	fmt.Println("purged: ", stored.GetPurgeTime().AsTime().Format(time.DateOnly))

	// GetBook still returns it; ListBooks hides it unless showDeleted.
	visible, _ := policy.Visible(stored, false)
	fmt.Println("get:", policy.CheckGet(stored) == nil, "list:", visible)

	// A second DeleteBook is NOT_FOUND.
	_, err := policy.Delete(stored)
	fmt.Println("delete again not found:", errors.Is(err, coreerrs.ErrNotFound))

	// UndeleteBook clears both times; undeleting again is ALREADY_EXISTS.
	if err = policy.Undelete(stored, nil); err != nil {
		panic(err)
	}

	err = policy.Undelete(stored, nil)
	fmt.Println("restored:", stored.GetDeleteTime() == nil, "again already exists:", errors.Is(err, coreerrs.ErrAlreadyExists))

	// Output:
	// deleted: 2026-11-07
	// purged:  2026-11-14
	// get: true list: false
	// delete again not found: true
	// restored: true again already exists: true
}

func ExamplePolicy_Restrict() {
	parser, _ := filterparser.New()
	node, _ := parser.Parse(context.Background(), `title == "Dune"`)

	policy := softdelete.New()
	hidden, _ := filter.Canonical(policy.Restrict(node, false))
	shown, _ := filter.Canonical(policy.Restrict(node, true))

	fmt.Println(hidden != shown)
	// Output: true
}

// countingStore answers every purge with two books.
type countingStore struct{}

func (countingStore) Preview(context.Context, string, filter.Node, int) (int64, []string, error) {
	return 2, []string{"shelves/1/books/1", "shelves/1/books/2"}, nil
}

func (countingStore) Purge(context.Context, string, filter.Node) (int64, error) { return 2, nil }

func ExamplePurger() {
	policy, _ := filter.NewPolicy(filter.WithAllowedFields("title"),
		filter.WithFieldTypes(map[string]filter.FieldKind{"title": filter.FieldKindString}))
	parser, _ := filterparser.New()

	purger, err := softdelete.NewPurger(policy, parser, countingStore{}, softdelete.WithParentRequired(true))
	if err != nil {
		panic(err)
	}

	req := &sdtestv1.PurgeBooksRequest{Parent: "shelves/1", Filter: `title == "Dune"`}

	resp, err := purger.Purge(context.Background(), softdelete.RequestFromProto(req))
	if err != nil {
		panic(err)
	}

	fmt.Println(resp.PurgeCount, resp.PurgeSample)

	_, err = purger.Purge(context.Background(), softdelete.PurgeRequest{Parent: "shelves/1"})
	fmt.Println(err)
	// Output:
	// 2 [shelves/1/books/1 shelves/1/books/2]
	// invalid argument: filter: is required
}
