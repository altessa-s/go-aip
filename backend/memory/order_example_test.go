// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory_test

import (
	"context"
	"fmt"
	"slices"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/orderby"
)

func ExampleOrderTranslator_Translate() {
	parser, err := orderby.NewParser()
	if err != nil {
		panic(err)
	}

	spec, err := parser.Parse(context.Background(), "score desc")
	if err != nil {
		panic(err)
	}

	policy, err := orderby.NewPolicy(orderby.WithAllowedFields("score"), orderby.WithUntrustedInput(true))
	if err != nil {
		panic(err)
	}

	tr, err := memory.NewOrderTranslator(policy, memory.WithOrderOptions(
		orderby.WithTiebreaker(orderby.Key{Name: "id"}),
	))
	if err != nil {
		panic(err)
	}

	compare, err := tr.Translate(spec)
	if err != nil {
		panic(err)
	}

	records := []map[string]any{
		{"id": "b", "score": 2},
		{"id": "c"},
		{"id": "a", "score": 2.5},
		{"id": "d", "score": 2},
	}
	slices.SortFunc(records, compare)

	for _, r := range records {
		fmt.Println(r["id"], r["score"])
	}

	// Output:
	// a 2.5
	// b 2
	// d 2
	// c <nil>
}
