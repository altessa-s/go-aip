// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package resourcename_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/resourcename"

	rntv1 "github.com/altessa-s/go-aip/internal/proto/gen/resourcenametest/v1"
)

func TestNewResource(t *testing.T) {
	t.Parallel()

	r, err := resourcename.NewResource("library.googleapis.com/Book", "publishers/{publisher}/books/{book}")
	require.NoError(t, err)
	assert.Equal(t, "library.googleapis.com/Book", r.Type())
	assert.Equal(t, "library.googleapis.com", r.Service())
	assert.Equal(t, "Book", r.Kind())
	assert.Equal(t, resourcename.DefaultNameField, r.NameField())
	assert.Empty(t, r.Singular())
	assert.Empty(t, r.Plural())
	require.Len(t, r.Patterns(), 1)
	require.Len(t, r.ParentPatterns(), 1)
	assert.Equal(t, "publishers/{publisher}", r.ParentPatterns()[0].String())
	assert.False(t, r.HasRootParent())
}

func TestNewResourceInvalid(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		typ      string
		patterns []string
		want     error
	}{
		"no slash":           {"Book", []string{"books/{book}"}, resourcename.ErrInvalidResource},
		"two slashes":        {"a.com/x/Book", []string{"books/{book}"}, resourcename.ErrInvalidResource},
		"lower kind":         {"a.com/book", []string{"books/{book}"}, resourcename.ErrInvalidResource},
		"kind with dash":     {"a.com/Bo-ok", []string{"books/{book}"}, resourcename.ErrInvalidResource},
		"bad service":        {"a_b.com/Book", []string{"books/{book}"}, resourcename.ErrInvalidResource},
		"empty kind":         {"a.com/", []string{"books/{book}"}, resourcename.ErrInvalidResource},
		"no pattern":         {"a.com/Book", nil, resourcename.ErrInvalidResource},
		"bad pattern":        {"a.com/Book", []string{"Books/{book}"}, resourcename.ErrInvalidPattern},
		"duplicate pattern":  {"a.com/Book", []string{"books/{book}", "books/{book}"}, resourcename.ErrInvalidResource},
		"same shape pattern": {"a.com/Book", []string{"books/{book}", "books/{volume}"}, resourcename.ErrInvalidResource},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := resourcename.NewResource(tt.typ, tt.patterns...)
			require.ErrorIs(t, err, tt.want)
		})
	}

	_, err := resourcename.ResourceFromDescriptor(nil)
	require.ErrorIs(t, err, resourcename.ErrInvalidResource)
}

func TestResourceMultiPattern(t *testing.T) {
	t.Parallel()

	r, err := resourcename.ResourceOf((&rntv1.Shelf{}).ProtoReflect().Descriptor())
	require.NoError(t, err)
	assert.Equal(t, "shelf", r.Singular())
	assert.Equal(t, "shelves", r.Plural())

	p, vals, err := r.Match("organizations/acme/shelves/s1")
	require.NoError(t, err)
	assert.Equal(t, "organizations/{organization}/shelves/{shelf}", p.String())
	assert.Equal(t, map[string]string{"organization": "acme", "shelf": "s1"}, vals)

	p, _, err = r.Match("projects/p1/shelves/s1")
	require.NoError(t, err)
	assert.Equal(t, "projects/{project}/shelves/{shelf}", p.String())

	_, _, err = r.Match("folders/f/shelves/s1")
	require.ErrorIs(t, err, resourcename.ErrNoMatch)
	assert.Contains(t, err.Error(), "none of the patterns")
	assert.False(t, r.Matches("folders/f/shelves/s1"))
	assert.True(t, r.Matches("projects/p/shelves/s"))

	parents := r.ParentPatterns()
	require.Len(t, parents, 2)
	assert.Equal(t, "projects/{project}", parents[0].String())
	assert.Equal(t, "organizations/{organization}", parents[1].String())
}

func TestResourceFirstMatchWins(t *testing.T) {
	t.Parallel()

	// "users/me/books/x" matches both patterns: the first declared wins
	// (AIP-123 appends new patterns, so the order is stable).
	r, err := resourcename.NewResource("library.example.com/Book", "users/me/books/{book}", "users/{user}/books/{book}")
	require.NoError(t, err)

	p, vals, err := r.Match("users/me/books/x")
	require.NoError(t, err)
	assert.Equal(t, "users/me/books/{book}", p.String())
	assert.Equal(t, map[string]string{"book": "x"}, vals)

	p, vals, err = r.Match("users/u1/books/x")
	require.NoError(t, err)
	assert.Equal(t, "users/{user}/books/{book}", p.String())
	assert.Equal(t, map[string]string{"user": "u1", "book": "x"}, vals)

	_, err = resourcename.NewResource("library.example.com/Book", "users/{user}/shelf/books/{book}", "users/{user}/{shelf}/books/{book}")
	require.ErrorIs(t, err, resourcename.ErrInvalidPattern)
}

func TestResourceParent(t *testing.T) {
	t.Parallel()

	shelf, err := resourcename.ResourceOf((&rntv1.Shelf{}).ProtoReflect().Descriptor())
	require.NoError(t, err)

	parent, err := shelf.Parent("organizations/acme/shelves/s1")
	require.NoError(t, err)
	assert.Equal(t, "organizations/acme", parent)

	publisher, err := resourcename.ResourceOf((&rntv1.Publisher{}).ProtoReflect().Descriptor())
	require.NoError(t, err)
	assert.True(t, publisher.HasRootParent())
	assert.Empty(t, publisher.ParentPatterns())

	parent, err = publisher.Parent("publishers/acme")
	require.NoError(t, err)
	assert.Empty(t, parent)

	config, err := resourcename.ResourceOf((&rntv1.PublisherConfig{}).ProtoReflect().Descriptor())
	require.NoError(t, err)

	parent, err = config.Parent("publishers/acme/config")
	require.NoError(t, err)
	assert.Equal(t, "publishers/acme", parent)

	_, err = config.Parent("publishers/acme")
	require.ErrorIs(t, err, resourcename.ErrNoMatch)

	edition, err := resourcename.ResourceOf((&rntv1.Edition{}).ProtoReflect().Descriptor())
	require.NoError(t, err)
	assert.Equal(t, "resource_name", edition.NameField())

	parent, err = edition.Parent("publishers/p/books/b/editions/e")
	require.NoError(t, err)
	assert.Equal(t, "publishers/p/books/b", parent)
}

func TestResourceOfErrors(t *testing.T) {
	t.Parallel()

	_, err := resourcename.ResourceOf((&rntv1.Plain{}).ProtoReflect().Descriptor())
	require.ErrorIs(t, err, resourcename.ErrNoResource)

	_, err = resourcename.ResourceOf((&rntv1.InvalidResource{}).ProtoReflect().Descriptor())
	require.ErrorIs(t, err, resourcename.ErrInvalidPattern)

	_, err = resourcename.ResourceOf((&fieldmaskpb.FieldMask{}).ProtoReflect().Descriptor())
	require.ErrorIs(t, err, resourcename.ErrNoResource)
}

func TestResourceFromDescriptorNameField(t *testing.T) {
	t.Parallel()

	r, err := resourcename.ResourceFromDescriptor(&annotations.ResourceDescriptor{
		Type: "library.example.com/Book", Pattern: []string{"books/{book}"}, NameField: "path",
	})
	require.NoError(t, err)
	assert.Equal(t, "path", r.NameField())
}
