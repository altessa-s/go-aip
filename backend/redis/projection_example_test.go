// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis_test

import (
	"fmt"

	"github.com/altessa-s/go-aip/backend/redis"
	"github.com/altessa-s/go-aip/projection"
)

func ExampleProjectionTranslator_Translate() {
	policy, _ := projection.NewPolicy(projection.WithAllowedFields("name", "email"))
	trans, _ := redis.NewProjectionTranslator(policy, redis.WithProjectionOptions(projection.WithRequiredFields("id")))

	args, _ := trans.Translate(projection.Spec{Paths: []string{"name"}})
	fmt.Println(args)

	args, _ = trans.Translate(projection.Spec{})
	fmt.Println(args)
	// Output:
	// [RETURN 2 id name]
	// [RETURN 3 email id name]
}
