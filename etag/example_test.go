// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package etag_test

import (
	"errors"
	"fmt"

	"google.golang.org/genproto/googleapis/api/annotations"

	"github.com/altessa-s/go-aip/etag"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/etagtest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// ExamplePrecondition checks the etag of a Delete request against the
// current revision of the resource.
func ExamplePrecondition() {
	pre := etag.NewPrecondition()
	current := etag.FromRevision[int64](8)

	req := &pb.DeleteBookRequest{Name: "shelves/1/books/2", Etag: `W/"7"`}

	sent, err := etag.Get(req)
	if err != nil {
		panic(err)
	}

	err = pre.Check(sent, current)
	fmt.Println(errors.Is(err, etag.ErrMismatch), errors.Is(err, coreerrs.ErrConflict))
	fmt.Println(pre.Check("", current))
	// Output:
	// true true
	// <nil>
}

// ExampleFromMessage issues a content etag that ignores the OUTPUT_ONLY
// fields a store rewrites, and stamps it into the response.
func ExampleFromMessage() {
	b := &pb.Book{Name: "shelves/1/books/2", Title: "Dune", UpdateTime: "2026-01-01T00:00:00Z"}

	tag, err := etag.FromMessage(b, etag.WithExcludeBehaviors(annotations.FieldBehavior_OUTPUT_ONLY), etag.WithWeak(true))
	if err != nil {
		panic(err)
	}

	if err = etag.Stamp(b, tag); err != nil {
		panic(err)
	}

	fmt.Println(tag.IsWeak(), b.GetEtag() == tag.String())
	// Output: true true
}

// ExamplePrecondition_ParseRevision hands the revision of an Update request
// to a store that compares it atomically.
func ExamplePrecondition_ParseRevision() {
	pre := etag.NewPrecondition(etag.WithField("book.etag"))

	rev, ok, err := pre.ParseRevision(etag.FromRevision[int64](5).String())
	fmt.Println(rev, ok, err)

	_, _, err = pre.ParseRevision(`"5"`)
	fmt.Println(err)
	// Output:
	// 5 true <nil>
	// invalid argument: book.etag: invalid value
}

// ExampleParse shows the accepted wire forms.
func ExampleParse() {
	for _, s := range []string{`"abc"`, `W/"abc"`, `abc`} {
		t, err := etag.Parse(s)
		fmt.Println(t.Value(), t.IsWeak(), err)
	}
	// Output:
	// abc false <nil>
	// abc true <nil>
	// abc false <nil>
}
