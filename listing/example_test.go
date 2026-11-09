// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing_test

import (
	"errors"
	"fmt"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

// widgets is a package-level schema: a declaration fixed in code, checked
// once at process start.
var widgets = listing.MustSchema(
	[]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "name", Kind: filter.FieldKindString},
		{Name: "createTime", Kind: filter.FieldKindTimestamp},
	},
	[]string{"createTime desc", "name"}, listing.NewProfile(listing.WithMaxPageSize(100)))

// ExampleMustSchema declares a schema at package level; the first pattern
// is the default order.
func ExampleMustSchema() {
	fmt.Println(widgets.Default())
	fmt.Println(widgets.Fields())
	fmt.Println(widgets.MaxPageSize())
	// Output:
	// createTime desc
	// [createTime id name]
	// 100
}

// ExampleMustSchema_invalid shows the panic of an invalid declaration: an
// error that wraps the NewSchema error.
func ExampleMustSchema_invalid() {
	defer func() {
		err, _ := recover().(error)
		fmt.Println(errors.Is(err, listing.ErrInvalidSchema))
	}()

	listing.MustSchema([]listing.Field{{Name: "name", Kind: filter.FieldKindString}}, []string{"gone"}, nil)
	// Output: true
}
