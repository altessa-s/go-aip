// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package state_test

import (
	"errors"
	"fmt"

	"github.com/altessa-s/go-aip/state"

	statetestv1 "github.com/altessa-s/go-aip/internal/proto/gen/statetest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func ExampleMachine_Transition() {
	machine := state.MustNew(map[statetestv1.Book_State][]statetestv1.Book_State{
		statetestv1.Book_DRAFT:     {statetestv1.Book_PUBLISHED, statetestv1.Book_DELETED},
		statetestv1.Book_PUBLISHED: {statetestv1.Book_ARCHIVED},
	})

	book := &statetestv1.Book{Name: "books/dune", State: statetestv1.Book_DRAFT}

	from, err := machine.Transition(book, "PublishBook", statetestv1.Book_PUBLISHED)
	fmt.Println(from, "->", book.GetState(), err)

	_, err = machine.Transition(book, "PublishBook", statetestv1.Book_PUBLISHED)
	fmt.Println(err, errors.Is(err, coreerrs.ErrFailedPrecondition))
	// Output:
	// DRAFT -> PUBLISHED <nil>
	// state: PublishBook: cannot move from PUBLISHED to PUBLISHED true
}

func ExampleLint() {
	for _, v := range state.Lint((&statetestv1.Shelf{}).ProtoReflect().Descriptor()) {
		fmt.Println(v.Rule, v.Element)
	}
	// Output:
	// state-output-only statetest.v1.Shelf.state
	// state-zero-value statetest.v1.Shelf.State
	// state-value-prefix statetest.v1.Shelf.STATE_ACTIVE
	// state-value-term statetest.v1.Shelf.READY
}
