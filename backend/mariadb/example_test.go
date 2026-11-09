// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb_test

import (
	"context"
	"fmt"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/projection"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

func ExampleNewFilterTranslator() {
	parser, _ := filterparser.New()
	ast, _ := parser.Parse(context.Background(), `status == 2 && type in [1, 2]`)

	policy, _ := filter.NewPolicy(filter.WithAllowedFields("status", "type"))
	trans, _ := mariadb.NewFilterTranslator(policy)

	where, args, _ := trans.Translate(ast)
	fmt.Println(where)
	fmt.Println(args)
	// Output:
	// (`status` = ?) AND (`type` IN (?, ?))
	// [2 1 2]
}

func ExampleNewOrderTranslator() {
	parser, _ := orderby.NewParser()
	spec, _ := parser.Parse(context.Background(), "createTime desc, name")

	policy, _ := orderby.NewPolicy(orderby.WithAllowedFields("createTime", "name"))
	trans, _ := mariadb.NewOrderTranslator(policy,
		sqlbase.WithOrderOptions(orderby.WithFieldMapping(map[string]string{"createTime": "created_at"})))

	order, _ := trans.Translate(spec)
	fmt.Println(order)
	// Output: `created_at` DESC, `name` ASC
}

func ExampleNewProjectionTranslator() {
	policy, _ := projection.NewPolicy(projection.WithAllowedFields("name", "createTime"))
	trans, _ := mariadb.NewProjectionTranslator(policy, sqlbase.WithProjectionOptions(
		projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
		projection.WithRequiredFields("id"),
	))

	cols, _ := trans.Translate(projection.Spec{Paths: []string{"createTime"}})
	fmt.Println(cols)
	// Output: `created_at`, `id`
}
