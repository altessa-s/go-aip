// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"fmt"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/projection"
)

func ExampleProjectionTranslator_Translate() {
	policy, _ := projection.NewPolicy(projection.WithAllowedFields("name", "createTime"))
	trans, _ := sqlbase.NewProjectionTranslator(policy, postgres.ProjectionDialect(), sqlbase.WithProjectionOptions(
		projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
		projection.WithRequiredFields("id"),
	))

	cols, _ := trans.Translate(projection.Spec{Paths: []string{"createTime"}})
	fmt.Println(cols)
	// Output: "created_at", "id"
}

func ExampleProjectionDialect() {
	policy, _ := projection.NewPolicy(projection.WithDeniedFields("password"))
	trans, _ := sqlbase.NewProjectionTranslator(policy, clickhouse.ProjectionDialect())

	cols, _ := trans.Translate(projection.Spec{})
	fmt.Println(cols)
	// Output: * EXCEPT (`password`)
}
