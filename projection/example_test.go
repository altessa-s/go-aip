// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/altessa-s/go-aip/projection"
)

func Example() {
	parser, _ := projection.NewParser()
	spec, _ := parser.Parse(context.Background(), "name, createTime, address.city")

	// The policy speaks API names and is shared by every backend.
	policy, err := projection.NewPolicy(
		projection.WithUntrustedInput(true),
		projection.WithAllowedFields("name", "createTime", "address.*", "credentials.login"),
		projection.WithDeniedFields("credentials.password"),
		// BASIC is a subset; FULL — every authorized field — needs no list.
		projection.WithView(projection.ViewBasic, "name"),
		// List methods should default to BASIC (AIP-157).
		projection.WithDefaultView(projection.ViewBasic),
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	fields, _ := policy.Validate(spec)
	fmt.Println(fields.Paths())

	// The translator context speaks storage names for one backend.
	storage, err := projection.NewTranslatorContext(policy,
		projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
		projection.WithRequiredFields("_id"),
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	sel, _ := storage.Resolve(spec)
	fmt.Println(sel.Include)

	// Validate and Resolve take a read mask alone: an omitted mask returns
	// every authorized field.
	all, _ := storage.Resolve(parser.MustParse(""))
	fmt.Println(all.Include)

	// ResolveView adds the view: an omitted mask defers to it, and
	// UNSPECIFIED to the default view.
	basic, _ := storage.ResolveView(parser.MustParse(""), projection.ViewUnspecified)
	fmt.Println(basic.Include)

	// A present mask wins over the view.
	masked, _ := storage.ResolveView(parser.MustParse("createTime"), projection.ViewBasic)
	fmt.Println(masked.Include)

	_, err = storage.Resolve(parser.MustParse("credentials"))
	fmt.Println(errors.Is(err, projection.ErrFieldNotAllowed))
	// Output:
	// [address.city createTime name]
	// [_id address.city created_at name]
	// [_id address created_at credentials.login name]
	// [_id name]
	// [_id created_at]
	// true
}
