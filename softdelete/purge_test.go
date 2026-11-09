// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package softdelete_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/softdelete"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
	sdtestv1 "github.com/altessa-s/go-aip/internal/proto/gen/softdeletetest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// fakeStore records the calls of a Purger and answers with fixed results.
type fakeStore struct {
	mu        sync.Mutex
	count     int64
	sample    []string
	err       error
	previews  []int // sample sizes
	purges    int
	lastNode  filter.Node
	lastScope string
}

func (s *fakeStore) Preview(_ context.Context, parent string, f filter.Node, sampleSize int) (int64, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.previews = append(s.previews, sampleSize)
	s.lastNode, s.lastScope = f, parent

	return s.count, s.sample, s.err
}

func (s *fakeStore) Purge(_ context.Context, parent string, f filter.Node) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.purges++
	s.lastNode, s.lastScope = f, parent

	return s.count, s.err
}

func newPurger(t *testing.T, store softdelete.Store, opts ...softdelete.PurgerOption) *softdelete.Purger {
	t.Helper()

	policy, err := filter.NewPolicy(
		filter.WithAllowedFields("title", "deleteTime"),
		filter.WithFieldTypes(map[string]filter.FieldKind{"title": filter.FieldKindString, "deleteTime": filter.FieldKindTimestamp}),
		filter.WithMaxDepth(4),
	)
	require.NoError(t, err)

	parser, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(t, err)

	p, err := softdelete.NewPurger(policy, parser, store, opts...)
	require.NoError(t, err)

	return p
}

func requireFieldError(t *testing.T, err error, field string) *coreerrs.FieldError {
	t.Helper()

	require.ErrorIs(t, err, coreerrs.ErrInvalidArgument)

	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok, "want a FieldError, got %v", err)
	assert.Equal(t, field, fe.Field)

	return fe
}

func TestPurgeValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		opts      []softdelete.PurgerOption
		req       softdelete.PurgeRequest
		wantField string
		wantCause error
	}{
		{name: "missing filter", req: softdelete.PurgeRequest{Parent: "shelves/1"}, wantField: softdelete.FieldFilter},
		{name: "blank filter", req: softdelete.PurgeRequest{Filter: "  \t"}, wantField: softdelete.FieldFilter},
		{
			name:      "parent required",
			opts:      []softdelete.PurgerOption{softdelete.WithParentRequired(true)},
			req:       softdelete.PurgeRequest{Filter: `title == "x"`},
			wantField: softdelete.FieldParent,
		},
		{
			name:      "parent checked before filter",
			opts:      []softdelete.PurgerOption{softdelete.WithParentRequired(true)},
			req:       softdelete.PurgeRequest{Parent: " "},
			wantField: softdelete.FieldParent,
		},
		{
			name:      "filter too long",
			opts:      []softdelete.PurgerOption{softdelete.WithMaxFilterLength(8)},
			req:       softdelete.PurgeRequest{Filter: `title == "abcdef"`},
			wantField: softdelete.FieldFilter,
			wantCause: softdelete.ErrFilterTooLong,
		},
		{name: "wildcard not enabled", req: softdelete.PurgeRequest{Filter: "*"}, wantField: softdelete.FieldFilter},
		{name: "syntax error", req: softdelete.PurgeRequest{Filter: `title ==`}, wantField: softdelete.FieldFilter},
		{
			name:      "field not allowed",
			req:       softdelete.PurgeRequest{Filter: `author == "x"`},
			wantField: softdelete.FieldFilter,
			wantCause: filter.ErrFieldNotAllowed,
		},
		{
			name:      "too deep",
			req:       softdelete.PurgeRequest{Filter: `!(!(!(!(!(title == "x")))))`},
			wantField: softdelete.FieldFilter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{count: 3}

			_, err := newPurger(t, store, tt.opts...).Purge(t.Context(), tt.req)
			requireFieldError(t, err, tt.wantField)

			if tt.wantCause != nil {
				require.ErrorIs(t, err, tt.wantCause)
			}

			assert.Empty(t, store.previews)
			assert.Zero(t, store.purges)
		})
	}
}

func TestPurgeFilterErrorHidesLiterals(t *testing.T) {
	t.Parallel()

	_, err := newPurger(t, &fakeStore{}).Purge(t.Context(), softdelete.PurgeRequest{Filter: `secret == "7707083893"`})
	fe := requireFieldError(t, err, softdelete.FieldFilter)
	assert.NotContains(t, fe.Reason, "7707083893")
	assert.NotContains(t, err.Error(), "7707083893")
}

func TestPurgeDryRun(t *testing.T) {
	t.Parallel()

	store := &fakeStore{count: 5, sample: []string{"books/1", "books/2", "books/3"}}
	p := newPurger(t, store, softdelete.WithSampleSize(2))

	resp, err := p.Purge(t.Context(), softdelete.PurgeRequest{Parent: "shelves/1", Filter: `title == "Dune"`})
	require.NoError(t, err)
	assert.Equal(t, int32(5), resp.PurgeCount)
	assert.Equal(t, []string{"books/1", "books/2"}, resp.PurgeSample, "a longer store sample is cut to the sample size")
	assert.Equal(t, []int{2}, store.previews)
	assert.Zero(t, store.purges)
	assert.Equal(t, "shelves/1", store.lastScope)
	assert.NotNil(t, store.lastNode)
}

func TestPurgeDryRunDefaults(t *testing.T) {
	t.Parallel()

	store := &fakeStore{count: 1, sample: []string{"books/1"}}

	resp, err := newPurger(t, store).Purge(t.Context(), softdelete.PurgeRequest{Filter: `title == "Dune"`})
	require.NoError(t, err)
	assert.Equal(t, []int{softdelete.DefaultSampleSize}, store.previews)
	assert.Equal(t, []string{"books/1"}, resp.PurgeSample)
}

func TestPurgeForce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		opts         []softdelete.PurgerOption
		count        int64
		wantPreviews []int
		wantPurges   int
		wantCount    int32
		wantErr      error
	}{
		{name: "counts then deletes", count: 7, wantPreviews: []int{0}, wantPurges: 1, wantCount: 7},
		{
			name:         "over the limit",
			opts:         []softdelete.PurgerOption{softdelete.WithMaxPurgeCount(5)},
			count:        6,
			wantPreviews: []int{0},
			wantErr:      softdelete.ErrTooManyResources,
		},
		{
			name:         "at the limit",
			opts:         []softdelete.PurgerOption{softdelete.WithMaxPurgeCount(6)},
			count:        6,
			wantPreviews: []int{0},
			wantPurges:   1,
			wantCount:    6,
		},
		{
			name:       "unlimited skips the count",
			opts:       []softdelete.PurgerOption{softdelete.WithMaxPurgeCount(0)},
			count:      math.MaxInt64,
			wantPurges: 1,
			wantCount:  math.MaxInt32,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{count: tt.count, sample: []string{"ignored"}}

			resp, err := newPurger(t, store, tt.opts...).Purge(t.Context(), softdelete.PurgeRequest{Filter: `title == "Dune"`, Force: true})
			if tt.wantErr != nil {
				requireFieldError(t, err, softdelete.FieldFilter)
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantCount, resp.PurgeCount)
				assert.Empty(t, resp.PurgeSample)
			}

			assert.Equal(t, tt.wantPreviews, store.previews)
			assert.Equal(t, tt.wantPurges, store.purges)
		})
	}
}

func TestPurgeWildcard(t *testing.T) {
	t.Parallel()

	store := &fakeStore{count: 2}

	resp, err := newPurger(t, store, softdelete.WithWildcard(true)).Purge(t.Context(), softdelete.PurgeRequest{Filter: " * ", Force: true})
	require.NoError(t, err)
	assert.Equal(t, int32(2), resp.PurgeCount)
	assert.Nil(t, store.lastNode, "the wildcard reaches the store as a nil filter")
}

func TestPurgeStoreErrors(t *testing.T) {
	t.Parallel()

	errStore := errors.New("store down")

	for _, force := range []bool{false, true} {
		for _, limit := range []int64{0, 10} {
			store := &fakeStore{err: errStore}
			_, err := newPurger(t, store, softdelete.WithMaxPurgeCount(limit)).
				Purge(t.Context(), softdelete.PurgeRequest{Filter: `title == "x"`, Force: force})
			require.ErrorIs(t, err, errStore)
		}

		store := &fakeStore{count: -1}
		_, err := newPurger(t, store, softdelete.WithMaxPurgeCount(0)).
			Purge(t.Context(), softdelete.PurgeRequest{Filter: `title == "x"`, Force: force})
		require.ErrorIs(t, err, softdelete.ErrInvalidStoreResult)
	}
}

// cancelParser fails like a parser whose context ended.
type cancelParser struct{}

func (cancelParser) Parse(ctx context.Context, _ string) (filter.Node, error) { return nil, ctx.Err() }

func TestPurgeContextErrorIsNotAFieldError(t *testing.T) {
	t.Parallel()

	policy, err := filter.NewPolicy(filter.WithAllowedFields("title"))
	require.NoError(t, err)

	p, err := softdelete.NewPurger(policy, cancelParser{}, &fakeStore{})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = p.Purge(ctx, softdelete.PurgeRequest{Filter: `title == "x"`})
	require.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, coreerrs.ErrInvalidArgument)
}

func TestNewPurgerRequiresDependencies(t *testing.T) {
	t.Parallel()

	policy, err := filter.NewPolicy(filter.WithAllowedFields("title"))
	require.NoError(t, err)

	parser, err := filterparser.New()
	require.NoError(t, err)

	var nilParser *filterparser.Parser

	for _, args := range []struct {
		policy *filter.Policy
		parser softdelete.Parser
		store  softdelete.Store
	}{
		{nil, parser, &fakeStore{}},
		{policy, nil, &fakeStore{}},
		{policy, nilParser, &fakeStore{}},
		{policy, parser, nil},
	} {
		_, err := softdelete.NewPurger(args.policy, args.parser, args.store)
		require.ErrorIs(t, err, softdelete.ErrInvalidPurger)
	}
}

func TestRequestFromProto(t *testing.T) {
	t.Parallel()

	assert.Equal(t, softdelete.PurgeRequest{Parent: "shelves/1", Filter: "f", Force: true},
		softdelete.RequestFromProto(&sdtestv1.PurgeBooksRequest{Parent: "shelves/1", Filter: "f", Force: true}))
	assert.Equal(t, softdelete.PurgeRequest{Filter: "g"},
		softdelete.RequestFromProto(&sdtestv1.PurgeTopicsRequest{Filter: "g"}))
}

func TestPurgeLimitReasonNamesNumbers(t *testing.T) {
	t.Parallel()

	_, err := newPurger(t, &fakeStore{count: 11}, softdelete.WithMaxPurgeCount(10)).
		Purge(t.Context(), softdelete.PurgeRequest{Filter: `title == "x"`, Force: true})
	fe := requireFieldError(t, err, softdelete.FieldFilter)
	assert.True(t, strings.Contains(fe.Reason, "11") && strings.Contains(fe.Reason, "10"), fe.Reason)
}
