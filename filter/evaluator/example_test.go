// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator_test

import (
	"context"
	"fmt"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/parser"
)

func ExampleEvaluator_Prepare() {
	p, err := parser.New()
	if err != nil {
		panic(err)
	}

	node, err := p.Parse(context.Background(), `age >= 18`)
	if err != nil {
		panic(err)
	}

	// The policy is built once per resource and shared with the translators.
	policy, err := filter.NewPolicy(filter.WithAllowedFields("age"))
	if err != nil {
		panic(err)
	}

	eval, err := evaluator.New(policy)
	if err != nil {
		panic(err)
	}

	prepared, err := eval.Prepare(node)
	if err != nil {
		panic(err)
	}

	matched, err := prepared.Evaluate(map[string]any{"age": int64(21)})
	fmt.Println(matched, err)
	// Output: true <nil>
}
