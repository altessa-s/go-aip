// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"context"
	"fmt"
	"log"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/orderby"
)

// ExampleOrderTranslator demonstrates the typical Parser → OrderTranslator chain for an
// AIP-132 order_by string targeted at MongoDB.
func ExampleOrderTranslator() {
	parser, err := orderby.NewParser()
	if err != nil {
		log.Fatal(err)
	}

	ob, err := parser.Parse(context.Background(), "create_time desc, slug")
	if err != nil {
		log.Fatal(err)
	}

	// The policy is built once per resource and shared by every backend.
	policy, err := orderby.NewPolicy(
		orderby.WithUntrustedInput(true),
		orderby.WithAllowedFields("create_time", "slug"),
	)
	if err != nil {
		log.Fatal(err)
	}

	trans, err := mongo.NewOrderTranslator(policy)
	if err != nil {
		log.Fatal(err)
	}

	sort, err := trans.Translate(ob)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(sort)
	// Output: {"create_time":{"$numberInt":"-1"},"slug":{"$numberInt":"1"}}
}

// ExampleNewOrderTranslator_untrustedInput shows the construction-time
// guards: a policy rejects WithUntrustedInput unless paired with a
// non-empty allow-list, and a translator cannot be built without a
// policy. The misconfiguration surfaces at boot rather than on the first
// request.
func ExampleNewOrderTranslator_untrustedInput() {
	_, err := orderby.NewPolicy(orderby.WithUntrustedInput(true))
	fmt.Println(err)

	_, err = mongo.NewOrderTranslator(nil)
	fmt.Println(err)
	// Output:
	// allowlist is required for untrusted input
	// order policy is required
}
