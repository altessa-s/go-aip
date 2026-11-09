// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"context"
	"fmt"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/parser"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/projection"
)

func ExampleNewFilterTranslator() {
	policy, err := filter.NewPolicy(filter.WithAllowedFields("status", "type"))
	if err != nil {
		panic(err)
	}

	trans, err := postgres.NewFilterTranslator(policy)
	if err != nil {
		panic(err)
	}

	p, err := parser.New()
	if err != nil {
		panic(err)
	}

	ast, err := p.Parse(context.Background(), `status == 2 && type in [1, 2]`)
	if err != nil {
		panic(err)
	}

	where, args, err := trans.Translate(ast)
	if err != nil {
		panic(err)
	}

	fmt.Println(where)
	fmt.Println(args)
	// Output:
	// ("status" = $1) AND ("type" IN ($2, $3))
	// [2 1 2]
}

// The same schema serves the SQL translator and the in-memory evaluator, so a row matches in memory exactly where
// the rendered WHERE clause holds.
func ExampleNewFilterTranslatorWithSchema() {
	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"amount": sqlbase.Scalar("amount", sqlbase.Numeric)})
	if err != nil {
		panic(err)
	}

	schema, err := postgres.NewSchema(fields)
	if err != nil {
		panic(err)
	}

	// One policy serves the SQL translator and the in-memory evaluator.
	policy, err := filter.NewPolicy(filter.WithAllowedFields("amount"))
	if err != nil {
		panic(err)
	}

	trans, err := postgres.NewFilterTranslatorWithSchema(policy, schema)
	if err != nil {
		panic(err)
	}

	eval, err := evaluator.New(policy, evaluator.WithProfile(schema), evaluator.WithValueDecoder(sqlbase.Decoder{}))
	if err != nil {
		panic(err)
	}

	p, err := parser.New()
	if err != nil {
		panic(err)
	}

	node, err := p.Parse(context.Background(), `amount == 1.00000000000000001`)
	if err != nil {
		panic(err)
	}

	where, args, err := trans.Translate(node)
	if err != nil {
		panic(err)
	}

	prepared, err := eval.Prepare(node)
	if err != nil {
		panic(err)
	}

	matched, err := prepared.Evaluate(map[string]any{"amount": "1.00000000000000001"})
	if err != nil {
		panic(err)
	}

	fmt.Println(where)
	fmt.Println(args)
	fmt.Println(matched)
	// Output:
	// ("amount" = ($1)::numeric)
	// [1.00000000000000001]
	// true
}

func ExampleNewOrderTranslator() {
	policy, err := orderby.NewPolicy(orderby.WithAllowedFields("createTime", "name"))
	if err != nil {
		panic(err)
	}

	trans, err := postgres.NewOrderTranslator(policy, sqlbase.WithOrderOptions(
		orderby.WithFieldMapping(map[string]string{"createTime": "created_at"}),
	))
	if err != nil {
		panic(err)
	}

	p, err := orderby.NewParser()
	if err != nil {
		panic(err)
	}

	spec, err := p.Parse(context.Background(), "createTime desc, name")
	if err != nil {
		panic(err)
	}

	body, err := trans.Translate(spec)
	if err != nil {
		panic(err)
	}

	fmt.Println(body)
	// Output: "created_at" DESC, "name" ASC
}

func ExampleNewProjectionTranslator() {
	policy, err := projection.NewPolicy(projection.WithAllowedFields("name", "createTime"))
	if err != nil {
		panic(err)
	}

	trans, err := postgres.NewProjectionTranslator(policy, sqlbase.WithProjectionOptions(
		projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
		projection.WithRequiredFields("id"),
	))
	if err != nil {
		panic(err)
	}

	cols, err := trans.Translate(projection.Spec{Paths: []string{"createTime"}})
	if err != nil {
		panic(err)
	}

	fmt.Println(cols)
	// Output: "created_at", "id"
}
