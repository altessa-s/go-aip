// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package batch_test

import (
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/batch"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// requireFieldError asserts err is an INVALID_ARGUMENT field error on field
// that matches cause.
func requireFieldError(t *testing.T, err error, field string, cause error) {
	t.Helper()

	require.Error(t, err)
	require.ErrorIs(t, err, cause)
	require.ErrorIs(t, err, coreerrs.ErrInvalidArgument)

	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok, "no field error in %v", err)
	assert.Equal(t, field, fe.Field)
}

func TestCheckSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		n     int
		opts  []batch.Option
		cause error
	}{
		{name: "within default", n: batch.DefaultMaxItems},
		{name: "over default", n: batch.DefaultMaxItems + 1, cause: batch.ErrTooManyItems},
		{name: "over custom", n: 3, opts: []batch.Option{batch.WithMaxItems(2)}, cause: batch.ErrTooManyItems},
		{name: "unlimited", n: 1 << 20, opts: []batch.Option{batch.WithMaxItems(0)}},
		{name: "negative keeps default", n: batch.DefaultMaxItems + 1, opts: []batch.Option{batch.WithMaxItems(-1)}, cause: batch.ErrTooManyItems},
		{name: "empty", n: 0, cause: batch.ErrEmpty},
		{name: "empty allowed", n: 0, opts: []batch.Option{batch.WithAllowEmpty(true)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := batch.CheckSize(batch.FieldRequests, tt.n, tt.opts...)
			if tt.cause == nil {
				require.NoError(t, err)
				return
			}

			requireFieldError(t, err, batch.FieldRequests, tt.cause)
		})
	}
}

func TestCheckSizeLimitFirst(t *testing.T) {
	t.Parallel()

	err := batch.CheckSize(batch.FieldNames, 5, batch.WithMaxItems(4), batch.WithAllowEmpty(true))
	requireFieldError(t, err, batch.FieldNames, batch.ErrTooManyItems)
	assert.Contains(t, err.Error(), "at most 4 items are allowed, got 5")
}

type createReq struct {
	parent string
}

func createParent(r createReq) (string, string) { return r.parent, "parent" }

func TestCheckParent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		parent string
		items  []createReq
		index  int
	}{
		{name: "no batch parent", items: []createReq{{parent: "publishers/1"}, {parent: "publishers/2"}}, index: -1},
		{name: "all match", parent: "publishers/1", items: []createReq{{parent: "publishers/1"}, {parent: "publishers/1"}}, index: -1},
		{name: "empty nested inherits", parent: "publishers/1", items: []createReq{{}, {parent: "publishers/1"}}, index: -1},
		{name: "mismatch", parent: "publishers/1", items: []createReq{{parent: "publishers/1"}, {parent: "publishers/2"}}, index: 1},
		{name: "first mismatch wins", parent: "publishers/1", items: []createReq{{parent: "publishers/3"}, {parent: "publishers/2"}}, index: 0},
		{name: "wildcard", parent: "publishers/-", items: []createReq{{parent: "publishers/1"}, {parent: "publishers/2"}}, index: -1},
		{name: "wildcard depth", parent: "publishers/-", items: []createReq{{parent: "publishers/1/shelves/2"}}, index: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := batch.CheckParent(batch.FieldRequests, tt.items, tt.parent, createParent)
			if tt.index < 0 {
				require.NoError(t, err)
				return
			}

			requireFieldError(t, err, "requests["+strconv.Itoa(tt.index)+"].parent", batch.ErrParentMismatch)

			ie, ok := errors.AsType[*batch.ItemError](err)
			require.True(t, ok)
			assert.Equal(t, tt.index, ie.Index)
			assert.Equal(t, batch.FieldRequests, ie.Field)
		})
	}
}

func TestCheckParentNames(t *testing.T) {
	t.Parallel()

	parentOf := func(name string) (string, string) {
		if len(name) > len("publishers/1") {
			return name[:len("publishers/1")], ""
		}

		return "", ""
	}

	err := batch.CheckParent(batch.FieldNames, []string{"publishers/1/books/1", "publishers/2/books/1"}, "publishers/1", parentOf)
	requireFieldError(t, err, "names[1]", batch.ErrParentMismatch)
	assert.Equal(t, "batch: names[1]: does not match the parent of the batch request", err.Error())

	require.NoError(t, batch.CheckParent[string](batch.FieldNames, []string{"x"}, "publishers/1", nil))
}

func TestParentMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		parent, item string
		want         bool
	}{
		{"publishers/1", "publishers/1", true},
		{"publishers/1", "publishers/2", false},
		{"publishers/-", "publishers/2", true},
		{"publishers/-/shelves/1", "publishers/2/shelves/1", true},
		{"publishers/-/shelves/1", "publishers/2/shelves/2", false},
		{"publishers/-", "publishers/2/shelves/1", false},
		{"-", "publishers", true},
		{"publishers/1", "", false},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, batch.ParentMatches(tt.parent, tt.item), "%q vs %q", tt.parent, tt.item)
	}
}

type updateReq struct {
	name string
	mask string
}

func TestCheckHoisted(t *testing.T) {
	t.Parallel()

	items := []updateReq{{mask: "title"}, {}, {mask: "author"}}
	nested := func(r updateReq) (string, string) { return r.mask, "updateMask" }

	err := batch.CheckHoisted(batch.FieldRequests, items, "title", nested)
	requireFieldError(t, err, "requests[2].updateMask", batch.ErrHoistedMismatch)

	require.NoError(t, batch.CheckHoisted(batch.FieldRequests, items, "", nested))
	require.NoError(t, batch.CheckHoisted(batch.FieldRequests, items[:2], "title", nested))
	require.NoError(t, batch.CheckHoisted[updateReq, string](batch.FieldRequests, items, "title", nil))
}

func TestCheckUnique(t *testing.T) {
	t.Parallel()

	keyOf := func(r updateReq) (string, string) { return r.name, "book.name" }

	err := batch.CheckUnique(batch.FieldRequests, []updateReq{{name: "a"}, {}, {}, {name: "b"}, {name: "a"}}, keyOf)
	requireFieldError(t, err, "requests[4].book.name", batch.ErrDuplicate)
	assert.Contains(t, err.Error(), "duplicates requests[0]")

	require.NoError(t, batch.CheckUnique(batch.FieldRequests, []updateReq{{name: "a"}, {name: "b"}, {}, {}}, keyOf))
	require.NoError(t, batch.CheckUnique[updateReq, string](batch.FieldRequests, []updateReq{{name: "a"}, {name: "a"}}, nil))

	ids := func(id int64) (int64, string) { return id, "" }
	err = batch.CheckUnique(batch.FieldNames, []int64{7, 8, 7}, ids)
	requireFieldError(t, err, "names[2]", batch.ErrDuplicate)
}

func TestEach(t *testing.T) {
	t.Parallel()

	errTitle := &coreerrs.FieldError{Field: "book.title", Reason: "is required"}
	check := func(i int, r updateReq) error {
		if r.name == "" {
			return errTitle
		}

		return nil
	}

	require.NoError(t, batch.Each(batch.FieldRequests, []updateReq{{name: "a"}}, check))

	err := batch.Each(batch.FieldRequests, []updateReq{{name: "a"}, {}, {}}, check)
	requireFieldError(t, err, "requests[1].book.title", coreerrs.ErrInvalidArgument)
	require.ErrorIs(t, err, errTitle)
	assert.Equal(t, "batch: requests[1].book.title: is required", err.Error())
}
