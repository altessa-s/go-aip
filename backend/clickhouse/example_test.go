// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse_test

import (
	"context"
	"fmt"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/parser"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/projection"
)

func ExampleNewFilterTranslator() {
	policy, err := filter.NewPolicy(filter.WithAllowedFields("status", "type"))
	if err != nil {
		panic(err)
	}

	trans, err := clickhouse.NewFilterTranslator(policy)
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
	// (`status` = ?) AND (`type` IN (?, ?))
	// [2 1 2]
}

func ExampleNewOrderTranslator() {
	policy, err := orderby.NewPolicy(orderby.WithAllowedFields("createTime", "name"))
	if err != nil {
		panic(err)
	}

	trans, err := clickhouse.NewOrderTranslator(policy, sqlbase.WithOrderOptions(
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
	// Output: `created_at` DESC, `name` ASC
}

func ExampleNewProjectionTranslator() {
	policy, err := projection.NewPolicy(projection.WithDeniedFields("password"))
	if err != nil {
		panic(err)
	}

	trans, err := clickhouse.NewProjectionTranslator(policy)
	if err != nil {
		panic(err)
	}

	cols, err := trans.Translate(projection.Spec{})
	if err != nil {
		panic(err)
	}

	fmt.Println(cols)
	// Output: * EXCEPT (`password`)
}

func ExampleNewFilterTranslatorWithSchema() {
	policy, err := filter.NewPolicy(filter.WithAllowedFields("name", "note", "tags", "price"))
	if err != nil {
		panic(err)
	}

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"name":  sqlbase.Scalar("name", sqlbase.Text),         // String, an absent value stored as ""
		"note":  sqlbase.PresenceScalar("note", sqlbase.Text), // Nullable(String)
		"tags":  sqlbase.Array("tags", sqlbase.Text),          // Array(String)
		"price": sqlbase.Scalar("price", sqlbase.Numeric),     // Decimal(P, S)
	})
	if err != nil {
		panic(err)
	}

	schema, err := clickhouse.NewSchema(fields, clickhouse.WithSchemaContractMode(true),
		clickhouse.WithSchemaDecimalFormat(sqlbase.DecimalFormat{Precision: 18, Scale: 2}))
	if err != nil {
		panic(err)
	}

	trans, err := clickhouse.NewFilterTranslatorWithSchema(policy, schema)
	if err != nil {
		panic(err)
	}

	p, err := parser.New()
	if err != nil {
		panic(err)
	}

	ast, err := p.Parse(context.Background(), `note != "x" && tags == "red" && price > 9.99 && name != null`)
	if err != nil {
		panic(err)
	}

	where, args, err := trans.Translate(ast)
	if err != nil {
		panic(err)
	}

	fmt.Println(where)
	fmt.Printf("%q\n", args)
	// Output:
	// (((`note` != ? OR `note` IS NULL) AND has(`tags`, ?)) AND ((`price` > CAST(? AS Decimal(18, 2))) AND (`name` != ?)))
	// ["x" "red" "9.99" ""]
}
