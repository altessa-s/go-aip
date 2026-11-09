// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

// ExampleFilterTranslator demonstrates the typical Parser → FilterTranslator chain for a CEL
// filter expression targeted at MongoDB.
func ExampleFilterTranslator() {
	parser, err := filterparser.New()
	if err != nil {
		log.Fatal(err)
	}

	ast, err := parser.Parse(context.Background(), `name == "John"`)
	if err != nil {
		log.Fatal(err)
	}

	// The policy names what a client may filter on; build it once per resource.
	policy, err := filter.NewPolicy(filter.WithAllowedFields("name"))
	if err != nil {
		log.Fatal(err)
	}

	trans, err := mongo.NewFilterTranslator(policy)
	if err != nil {
		log.Fatal(err)
	}

	bsonFilter, err := trans.Translate(ast)
	if err != nil {
		log.Fatal(err)
	}

	out, _ := json.Marshal(bsonFilter)
	fmt.Println(string(out))
	// Output: {"name":"John"}
}

// ExampleNewFilterTranslator_untrustedInput shows the construction-time
// guard that rejects [filter.WithUntrustedInput] unless paired with a
// non-empty [filter.WithAllowedFields] allow-list. A translator needs a
// policy, so the misconfiguration surfaces at boot rather than on the
// first request.
func ExampleNewFilterTranslator_untrustedInput() {
	_, err := filter.NewPolicy(filter.WithUntrustedInput(true))
	fmt.Println(err)
	// Output: filter field allowlist is required
}
