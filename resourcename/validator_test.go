// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package resourcename_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/altessa-s/go-aip/resourcename"

	rntv1 "github.com/altessa-s/go-aip/internal/proto/gen/resourcenametest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func newFixtureValidator(t *testing.T, opts ...resourcename.Option) *resourcename.Validator {
	t.Helper()

	v := resourcename.NewValidator(opts...)
	require.NoError(t, v.RegisterFile(rntv1.File_resourcenametest_v1_test_proto))

	return v
}

func lookup(t *testing.T, v *resourcename.Validator, typ string) *resourcename.Resource {
	t.Helper()

	r, ok := v.Lookup(typ)
	require.True(t, ok, typ)

	return r
}

// requireFieldError asserts err is a field error on field with the cause.
func requireFieldError(t *testing.T, err error, field string, cause error) *resourcename.NameError {
	t.Helper()

	require.Error(t, err)
	require.ErrorIs(t, err, coreerrs.ErrInvalidArgument)
	require.ErrorIs(t, err, resourcename.ErrInvalidName)
	require.ErrorIs(t, err, cause)

	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok)
	assert.Equal(t, field, fe.Field)
	assert.NotEmpty(t, fe.Reason)

	ne, ok := errors.AsType[*resourcename.NameError](err)
	require.True(t, ok)
	assert.Equal(t, ne.Reason, fe.Reason)

	return ne
}

func TestValidatorRegisterFile(t *testing.T) {
	t.Parallel()

	v := newFixtureValidator(t)

	for _, typ := range []string{
		"library.example.com/Project", "library.example.com/Organization", "library.example.com/Publisher",
		"library.example.com/Book", "library.example.com/Shelf", "library.example.com/PublisherConfig",
		"library.example.com/Edition",
	} {
		_, ok := v.Lookup(typ)
		assert.True(t, ok, typ)
	}

	_, ok := v.Lookup("library.example.com/Missing")
	assert.False(t, ok)

	// Registering the same file again is a no-op.
	require.NoError(t, v.RegisterFile(rntv1.File_resourcenametest_v1_test_proto))

	err := v.RegisterFile(rntv1.File_resourcenametest_v1_invalid_proto)
	require.ErrorIs(t, err, resourcename.ErrInvalidPattern)
}

func TestValidatorRegisterDuplicate(t *testing.T) {
	t.Parallel()

	v := resourcename.NewValidator()
	a, err := resourcename.NewResource("library.example.com/Book", "books/{book}")
	require.NoError(t, err)
	b, err := resourcename.NewResource("library.example.com/Book", "shelves/{shelf}/books/{book}")
	require.NoError(t, err)
	c, err := resourcename.NewResource("library.example.com/Shelf", "shelves/{shelf}")
	require.NoError(t, err)

	require.NoError(t, v.Register(a))
	require.NoError(t, v.Register(a))
	require.ErrorIs(t, v.Register(c, b), resourcename.ErrDuplicateType)

	_, ok := v.Lookup(c.Type())
	assert.False(t, ok, "a failed Register registers nothing")

	require.ErrorIs(t, resourcename.NewValidator().Register(b, a), resourcename.ErrDuplicateType)
	require.ErrorIs(t, v.Register(nil), resourcename.ErrInvalidResource)
}

func TestValidatorRegisterFiles(t *testing.T) {
	t.Parallel()

	v := resourcename.NewValidator()
	err := v.RegisterFiles(nil) // GlobalFiles holds the invalid fixture.
	require.ErrorIs(t, err, resourcename.ErrInvalidPattern)

	_, ok := v.Lookup("library.example.com/Book")
	assert.True(t, ok, "valid files are registered despite the invalid one")

	files := new(protoregistry.Files)
	require.NoError(t, files.RegisterFile(rntv1.File_resourcenametest_v1_test_proto))
	require.NoError(t, resourcename.NewValidator().RegisterFiles(files))
}

func TestCheckName(t *testing.T) {
	t.Parallel()

	v := newFixtureValidator(t)
	book := lookup(t, v, "library.example.com/Book")

	require.NoError(t, v.CheckName("name", "publishers/acme/books/les-miserables", book))
	require.NoError(t, v.CheckName("name", "publishers/Acme/books/Les Miserables", book), "permissive IDs")

	requireFieldError(t, v.CheckName("name", "", book), "name", resourcename.ErrNoMatch)
	ne := requireFieldError(t, v.CheckName("name", "publishers/acme/novels/x", book), "name", resourcename.ErrNoMatch)
	assert.Equal(t, 2, ne.Segment)
	requireFieldError(t, v.CheckName("name", "publishers/acme", book), "name", resourcename.ErrNoMatch)
	ne = requireFieldError(t, v.CheckName("name", "publishers/../books/x", book), "name", resourcename.ErrInvalidID)
	assert.Equal(t, 1, ne.Segment)
	requireFieldError(t, v.CheckName("name", "publishers/a\x00/books/x", book), "name", resourcename.ErrInvalidID)

	require.ErrorIs(t, v.CheckName("name", "x", nil), resourcename.ErrInvalidResource)
}

func TestCheckNameStrictIDs(t *testing.T) {
	t.Parallel()

	v := newFixtureValidator(t, resourcename.WithIDValidator(resourcename.StrictID))
	book := lookup(t, v, "library.example.com/Book")

	require.NoError(t, v.CheckName("name", "publishers/acme/books/les-miserables", book))

	ne := requireFieldError(t, v.CheckName("name", "publishers/Acme/books/x", book), "name", resourcename.ErrInvalidID)
	assert.Equal(t, 1, ne.Segment)
	assert.Contains(t, ne.Reason, "publisher ID")
	assert.Contains(t, ne.Reason, "RFC-1034")

	ne = requireFieldError(t, v.CheckName("name", "publishers/acme/books/1984", book), "name", resourcename.ErrInvalidID)
	assert.Equal(t, 3, ne.Segment)

	nilValidator := newFixtureValidator(t, resourcename.WithIDValidator(nil))
	require.NoError(t, nilValidator.CheckName("name", "publishers/Acme/books/x", book), "nil keeps the permissive default")
}

func TestCheckNameWildcard(t *testing.T) {
	t.Parallel()

	v := newFixtureValidator(t)
	book := lookup(t, v, "library.example.com/Book")

	tests := []struct {
		name     string
		value    string
		policy   resourcename.WildcardPolicy
		wantErr  bool
		wantSegm int
	}{
		{"none refuses ancestor", "publishers/-/books/x", resourcename.WildcardNone, true, 1},
		{"none refuses id", "publishers/acme/books/-", resourcename.WildcardNone, true, 3},
		{"ancestors accepts ancestor", "publishers/-/books/x", resourcename.WildcardAncestors, false, 0},
		{"ancestors refuses id", "publishers/-/books/-", resourcename.WildcardAncestors, true, 3},
		{"any accepts all", "publishers/-/books/-", resourcename.WildcardAny, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := v.CheckName("name", tt.value, book, resourcename.WithWildcard(tt.policy))
			if !tt.wantErr {
				require.NoError(t, err)

				return
			}

			ne := requireFieldError(t, err, "name", resourcename.ErrWildcard)
			assert.Equal(t, tt.wantSegm, ne.Segment)
		})
	}
}

func TestCheckParent(t *testing.T) {
	t.Parallel()

	v := newFixtureValidator(t, resourcename.WithIDValidator(resourcename.StrictID))
	book := lookup(t, v, "library.example.com/Book")
	shelf := lookup(t, v, "library.example.com/Shelf")
	publisher := lookup(t, v, "library.example.com/Publisher")

	require.NoError(t, v.CheckParent("parent", "publishers/acme", book))
	require.NoError(t, v.CheckParent("parent", "projects/p1", shelf))
	require.NoError(t, v.CheckParent("parent", "organizations/o1", shelf))
	require.NoError(t, v.CheckParent("parent", "", publisher), "the root is the parent of a top-level collection")

	requireFieldError(t, v.CheckParent("parent", "", book), "parent", resourcename.ErrNoMatch)
	ne := requireFieldError(t, v.CheckParent("parent", "folders/f1", shelf), "parent", resourcename.ErrNoMatch)
	assert.Contains(t, ne.Reason, "the parent of library.example.com/Shelf")
	ne = requireFieldError(t, v.CheckParent("parent", "users/u1", book), "parent", resourcename.ErrNoMatch)
	assert.Contains(t, ne.Reason, `"publishers/{publisher}"`)
	ne = requireFieldError(t, v.CheckParent("parent", "publishers/acme", publisher), "parent", resourcename.ErrNoMatch)
	assert.Contains(t, ne.Reason, "root")

	// AIP-159: a List parent may read across collections.
	requireFieldError(t, v.CheckParent("parent", "publishers/-", book), "parent", resourcename.ErrWildcard)
	require.NoError(t, v.CheckParent("parent", "publishers/-", book, resourcename.WithWildcard(resourcename.WildcardAny)))
	requireFieldError(t, v.CheckParent("parent", "publishers/-", book, resourcename.WithWildcard(resourcename.WildcardAncestors)),
		"parent", resourcename.ErrWildcard)
	require.ErrorIs(t, v.CheckParent("parent", "x", nil), resourcename.ErrInvalidResource)
}

func TestCheckFullNames(t *testing.T) {
	t.Parallel()

	relative := newFixtureValidator(t)
	full := newFixtureValidator(t, resourcename.WithFullNames(true))
	book := lookup(t, full, "library.example.com/Book")

	name := "//library.example.com/publishers/acme/books/dune"
	requireFieldError(t, relative.CheckName("name", name, book), "name", resourcename.ErrInvalidFullName)
	require.NoError(t, full.CheckName("name", name, book))
	require.NoError(t, full.CheckName("name", "publishers/acme/books/dune", book), "relative names stay valid")

	requireFieldError(t, full.CheckName("name", "//other.example.com/publishers/acme/books/dune", book), "name", resourcename.ErrInvalidFullName)
	requireFieldError(t, full.CheckName("name", "//library.example.com", book), "name", resourcename.ErrInvalidFullName)
	requireFieldError(t, full.CheckName("name", "//library.example.com/publishers/acme", book), "name", resourcename.ErrNoMatch)
	require.NoError(t, full.CheckParent("parent", "//library.example.com/publishers/acme", book))
}

func TestCheckLimits(t *testing.T) {
	t.Parallel()

	v := newFixtureValidator(t, resourcename.WithMaxNameLength(30), resourcename.WithMaxSegments(4))
	book := lookup(t, v, "library.example.com/Book")
	edition := lookup(t, v, "library.example.com/Edition")

	require.NoError(t, v.CheckName("name", "publishers/a/books/b", book))
	requireFieldError(t, v.CheckName("name", "publishers/"+strings.Repeat("a", 20)+"/books/b", book), "name", resourcename.ErrLimitExceeded)
	requireFieldError(t, v.CheckName("name", "publishers/p/books/b/editions/e", edition), "name", resourcename.ErrLimitExceeded)
	requireFieldError(t, v.CheckName("name", strings.Repeat("a/", 10)+"a", book), "name", resourcename.ErrLimitExceeded)

	unlimited := newFixtureValidator(t, resourcename.WithMaxNameLength(0), resourcename.WithMaxSegments(0))
	long := "publishers/" + strings.Repeat("a", 10000) + "/books/b"
	require.NoError(t, unlimited.CheckName("name", long, book))

	defaults := newFixtureValidator(t, resourcename.WithMaxNameLength(-1), resourcename.WithMaxSegments(-1))
	requireFieldError(t, defaults.CheckName("name", long, book), "name", resourcename.ErrLimitExceeded)
	requireFieldError(t, defaults.CheckName("name", strings.Repeat("a/", resourcename.DefaultMaxSegments)+"a", book), "name",
		resourcename.ErrLimitExceeded)
}

func TestValidateRequest(t *testing.T) {
	t.Parallel()

	v := newFixtureValidator(t, resourcename.WithIDValidator(resourcename.StrictID))
	across := resourcename.WithWildcard(resourcename.WildcardAny)

	tests := []struct {
		name  string
		msg   proto.Message
		opts  []resourcename.CheckOption
		field string
		cause error
	}{
		{name: "get", msg: &rntv1.GetBookRequest{Name: "publishers/acme/books/dune"}},
		{name: "get empty skipped", msg: &rntv1.GetBookRequest{}},
		{name: "get mismatch", msg: &rntv1.GetBookRequest{Name: "publishers/acme"}, field: "name", cause: resourcename.ErrNoMatch},
		{name: "get bad id", msg: &rntv1.GetBookRequest{Name: "publishers/ACME/books/dune"}, field: "name", cause: resourcename.ErrInvalidID},
		{name: "get wildcard", msg: &rntv1.GetBookRequest{Name: "publishers/-/books/dune"}, field: "name", cause: resourcename.ErrWildcard},
		{name: "get unique lookup", msg: &rntv1.GetBookRequest{Name: "publishers/-/books/dune"},
			opts: []resourcename.CheckOption{resourcename.WithWildcard(resourcename.WildcardAncestors)}},
		{name: "list", msg: &rntv1.ListBooksRequest{Parent: "publishers/acme"}},
		{name: "list across publishers", msg: &rntv1.ListBooksRequest{Parent: "publishers/-"}, opts: []resourcename.CheckOption{across}},
		{name: "list wildcard refused", msg: &rntv1.ListBooksRequest{Parent: "publishers/-"}, field: "parent", cause: resourcename.ErrWildcard},
		{name: "list wrong parent", msg: &rntv1.ListBooksRequest{Parent: "publishers/acme/books/x"}, field: "parent", cause: resourcename.ErrNoMatch},
		{name: "list shelves org", msg: &rntv1.ListShelvesRequest{Parent: "organizations/o1"}},
		{name: "list shelves bad", msg: &rntv1.ListShelvesRequest{Parent: "folders/f1"}, field: "parent", cause: resourcename.ErrNoMatch},
		{name: "list top-level", msg: &rntv1.ListPublishersRequest{}},
		{name: "list top-level with parent", msg: &rntv1.ListPublishersRequest{Parent: "publishers/x"}, field: "parent", cause: resourcename.ErrNoMatch},
		{name: "create", msg: &rntv1.CreateBookRequest{Parent: "publishers/acme", Book: &rntv1.Book{Title: "Dune"}}},
		{name: "create bad parent", msg: &rntv1.CreateBookRequest{Parent: "publishers/acme/books/x"}, field: "parent", cause: resourcename.ErrNoMatch},
		{name: "create resource name checked", msg: &rntv1.CreateBookRequest{Parent: "publishers/acme", Book: &rntv1.Book{Name: "books/x"}},
			field: "book.name", cause: resourcename.ErrNoMatch},
		{name: "update", msg: &rntv1.UpdateBookRequest{Book: &rntv1.Book{Name: "publishers/acme/books/dune"}}},
		{name: "update no resource", msg: &rntv1.UpdateBookRequest{}},
		{name: "update wildcard never", msg: &rntv1.UpdateBookRequest{Book: &rntv1.Book{Name: "publishers/-/books/dune"}},
			opts: []resourcename.CheckOption{across}, field: "book.name", cause: resourcename.ErrWildcard},
		{name: "update custom name field", msg: &rntv1.UpdateEditionRequest{Edition: &rntv1.Edition{ResourceName: "publishers/p/books/b"}},
			field: "edition.resourceName", cause: resourcename.ErrNoMatch},
		{name: "update custom name field ok", msg: &rntv1.UpdateEditionRequest{Edition: &rntv1.Edition{ResourceName: "publishers/p/books/b/editions/e"}}},
		{name: "batch get", msg: &rntv1.BatchGetBooksRequest{Parent: "publishers/p", Names: []string{"publishers/p/books/a", "publishers/p/books/b"}}},
		{name: "batch get bad element", msg: &rntv1.BatchGetBooksRequest{Names: []string{"publishers/p/books/a", "", "shelves/x"}},
			field: "names[2]", cause: resourcename.ErrNoMatch},
		{name: "singleton", msg: &rntv1.GetPublisherConfigRequest{Name: "publishers/p/config"}},
		{name: "singleton bad", msg: &rntv1.GetPublisherConfigRequest{Name: "publishers/p/settings"}, field: "name", cause: resourcename.ErrNoMatch},
		{name: "any type unchecked", msg: &rntv1.AnyResourceRequest{Resource: "whatever//x"}},
		{name: "plain", msg: &rntv1.Plain{Name: "anything"}},
		{name: "nil message", msg: (*rntv1.GetBookRequest)(nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := v.ValidateRequest(tt.msg, tt.opts...)
			if tt.cause == nil {
				require.NoError(t, err)

				return
			}

			requireFieldError(t, err, tt.field, tt.cause)
		})
	}
}

func TestValidateRequestUnknownType(t *testing.T) {
	t.Parallel()

	v := newFixtureValidator(t)

	err := v.ValidateRequest(&rntv1.UnknownTypeRequest{Name: "x"})
	require.ErrorIs(t, err, resourcename.ErrUnknownType)
	require.NotErrorIs(t, err, coreerrs.ErrInvalidArgument, "a configuration error is not a client error")

	missing, err := resourcename.NewResource("library.example.com/Missing", "missing/{missing}")
	require.NoError(t, err)
	require.NoError(t, v.Register(missing))
	require.NoError(t, v.ValidateRequest(&rntv1.UnknownTypeRequest{Name: "missing/x"}), "registering drops the cached plan")
}

func TestValidateRequestUnregisteredResourceMessage(t *testing.T) {
	t.Parallel()

	// The resource of an Update is read from its message even when its type
	// is not registered.
	v := resourcename.NewValidator()
	require.NoError(t, v.ValidateRequest(&rntv1.UpdateBookRequest{Book: &rntv1.Book{Name: "publishers/a/books/b"}}))

	err := v.ValidateRequest(&rntv1.UpdateBookRequest{Book: &rntv1.Book{Name: "books/b"}})
	requireFieldError(t, err, "book.name", resourcename.ErrNoMatch)
}

func TestValidatorConcurrent(t *testing.T) {
	t.Parallel()

	v := newFixtureValidator(t)
	extra, err := resourcename.NewResource("library.example.com/Extra", "extras/{extra}")
	require.NoError(t, err)

	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			for range 100 {
				assert.NoError(t, v.ValidateRequest(&rntv1.GetBookRequest{Name: "publishers/a/books/b"}))
				assert.Error(t, v.ValidateRequest(&rntv1.ListBooksRequest{Parent: "x/y"}))
			}
		})
	}

	wg.Go(func() {
		for range 50 {
			assert.NoError(t, v.Register(extra))
		}
	})

	wg.Wait()
}
