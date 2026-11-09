// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili_test

import (
	"fmt"

	"github.com/altessa-s/go-aip/backend/meili"
	"github.com/altessa-s/go-aip/projection"
)

func ExampleProjectionTranslator_Translate() {
	policy, _ := projection.NewPolicy(projection.WithAllowedFields("title", "author.*"))
	trans, _ := meili.NewProjectionTranslator(policy, meili.WithProjectionOptions(projection.WithRequiredFields("id")))

	attrs, _ := trans.Translate(projection.Spec{Paths: []string{"author.name"}})
	fmt.Println(attrs)

	attrs, _ = trans.Translate(projection.Spec{})
	fmt.Println(attrs)
	// Output:
	// [author.name id]
	// [author id title]
}
