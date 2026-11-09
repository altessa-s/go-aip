// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package resourcename_test

import (
	"errors"
	"fmt"

	"github.com/altessa-s/go-aip/resourcename"

	rntv1 "github.com/altessa-s/go-aip/internal/proto/gen/resourcenametest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func ExamplePattern() {
	book := resourcename.MustParsePattern("publishers/{publisher}/books/{book}")

	vals, err := book.Match("publishers/acme/books/les-miserables")
	fmt.Println(vals["publisher"], vals["book"], err)

	name, err := book.Format(map[string]string{"publisher": "acme", "book": "dune"})
	fmt.Println(name, err)

	parent, _ := book.Parent()
	fmt.Println(parent)

	// Output:
	// acme les-miserables <nil>
	// publishers/acme/books/dune <nil>
	// publishers/{publisher}
}

func ExampleResource() {
	shelf, err := resourcename.NewResource("library.example.com/Shelf",
		"projects/{project}/shelves/{shelf}",
		"organizations/{organization}/shelves/{shelf}",
	)
	if err != nil {
		panic(err)
	}

	p, vals, _ := shelf.Match("organizations/acme/shelves/s1")
	fmt.Println(p, vals["organization"])

	parent, _ := shelf.Parent("organizations/acme/shelves/s1")
	fmt.Println(parent)

	// Output:
	// organizations/{organization}/shelves/{shelf} acme
	// organizations/acme
}

func ExampleValidator_ValidateRequest() {
	v := resourcename.NewValidator(resourcename.WithIDValidator(resourcename.StrictID))
	if err := v.RegisterFile(rntv1.File_resourcenametest_v1_test_proto); err != nil {
		panic(err)
	}

	// Get: the name must be a Book.
	err := v.ValidateRequest(&rntv1.GetBookRequest{Name: "publishers/acme/books"})
	if fe, ok := errors.AsType[*coreerrs.FieldError](err); ok {
		fmt.Println(fe.Field, "-", fe.Reason)
	}

	// List across every publisher (AIP-159).
	err = v.ValidateRequest(&rntv1.ListBooksRequest{Parent: "publishers/-"},
		resourcename.WithWildcard(resourcename.WildcardAny))
	fmt.Println(err)

	// Without the wildcard policy "-" is refused.
	err = v.ValidateRequest(&rntv1.ListBooksRequest{Parent: "publishers/-"})
	fmt.Println(errors.Is(err, resourcename.ErrWildcard))

	// Output:
	// name - the name has 3 segments, pattern "publishers/{publisher}/books/{book}" has 4
	// <nil>
	// true
}

func ExampleFullName() {
	full, _ := resourcename.FullName("library.googleapis.com", "publishers/123/books/les-miserables")
	fmt.Println(full)

	service, name, _ := resourcename.ParseFullName(full)
	fmt.Println(service, name)

	// Output:
	// //library.googleapis.com/publishers/123/books/les-miserables
	// library.googleapis.com publishers/123/books/les-miserables
}
