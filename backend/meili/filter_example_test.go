// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili_test

import (
	"context"
	"fmt"

	"github.com/altessa-s/go-aip/backend/meili"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/parser"
)

func ExampleNewFilterTranslator() {
	policy, err := filter.NewPolicy(filter.WithAllowedFields("name", "tags", "deletedAt"), filter.WithArrayFields("tags"))
	if err != nil {
		panic(err)
	}

	trans, err := meili.NewFilterTranslator(policy, meili.WithFilterServerVersion(meili.Version{Major: 1, Minor: 17}))
	if err != nil {
		panic(err)
	}

	p, err := parser.New()
	if err != nil {
		panic(err)
	}

	for _, expr := range []string{
		`"red" in tags && deletedAt == null`,
		`name.startsWith("Jo") && tags.size() > 0`,
		`name.contains("oh")`,
	} {
		ast, err := p.Parse(context.Background(), expr)
		if err != nil {
			panic(err)
		}

		out, err := trans.Translate(ast)
		fmt.Println(out, err)
	}
	// Output:
	// (tags = "red") AND ((deletedAt IS NULL OR deletedAt NOT EXISTS)) <nil>
	// (name STARTS WITH "Jo") AND ((tags EXISTS AND tags IS NOT NULL AND tags IS NOT EMPTY)) <nil>
	//  contains() needs the experimental containsFilter feature (Meilisearch 1.10 or later, WithFilterContainsFilter): unsupported operation
}
