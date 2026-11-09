// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory_test

import (
	"fmt"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/projection"
)

func ExampleProjection_Apply() {
	policy, _ := projection.NewPolicy(projection.WithAllowedFields("name", "items.*"))
	trans, _ := memory.NewProjectionTranslator(policy, memory.WithProjectionOptions(projection.WithRequiredFields("id")))

	proj, _ := trans.Translate(projection.Spec{Paths: []string{"items.sku"}})
	fmt.Println(proj.Apply(map[string]any{
		"id":    1,
		"name":  "order",
		"items": []any{map[string]any{"sku": "a", "price": 2}, map[string]any{"sku": "b"}},
	}))
	// Output: map[id:1 items:[map[sku:a] map[sku:b]]]
}
