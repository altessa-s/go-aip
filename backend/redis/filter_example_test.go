// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis_test

import (
	"context"
	"fmt"

	"github.com/altessa-s/go-aip/backend/redis"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/parser"
)

func ExampleNewFilterTranslatorWithSchema() {
	policy, err := filter.NewPolicy(filter.WithAllowedFields("status", "priority", "title"))
	if err != nil {
		panic(err)
	}

	schema, err := redis.NewSchema(map[string]redis.FieldType{
		"status":   redis.FieldTypeTag,
		"priority": redis.FieldTypeNumeric,
		"title":    redis.FieldTypeText,
	})
	if err != nil {
		panic(err)
	}

	trans, err := redis.NewFilterTranslatorWithSchema(policy, schema)
	if err != nil {
		panic(err)
	}

	p, err := parser.New()
	if err != nil {
		panic(err)
	}

	ast, err := p.Parse(context.Background(), `status == "open" && priority >= 3 && title.startsWith("db")`)
	if err != nil {
		panic(err)
	}

	query, err := trans.Translate(ast)
	if err != nil {
		panic(err)
	}

	fmt.Println(query)
	// Output:
	// ((@status:{open} @priority:[3 +inf]) @title:db*)
}
