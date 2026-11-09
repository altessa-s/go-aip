// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package unreachable_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/unreachable"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestValidateName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		ok    bool
	}{
		{name: "location", input: "projects/p/locations/us-east1", ok: true},
		{name: "resource", input: "shelves/scifi1/books/starwars4", ok: true},
		{name: "singleton", input: "users/1/config", ok: true},
		{name: "empty", input: ""},
		{name: "full name", input: "//library.googleapis.com/shelves/scifi1/books/starwars4"},
		{name: "uri", input: "https://library.googleapis.com/v1/shelves/scifi1"},
		{name: "resource id", input: "starwars4"},
		{name: "leading slash", input: "/shelves/scifi1"},
		{name: "trailing slash", input: "shelves/scifi1/"},
		{name: "empty segment", input: "shelves//books/b"},
		{name: "whitespace", input: "shelves/sci fi"},
		{name: "control", input: "shelves/sci\x7f"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := unreachable.ValidateName(tt.input)
			if tt.ok {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, unreachable.ErrInvalidName)
		})
	}
}

func TestSetDedupSortAndResponse(t *testing.T) {
	t.Parallel()

	s := unreachable.NewSet()
	require.NoError(t, s.Add("projects/p/locations/us-west1", "projects/p/locations/europe-west2"))
	require.NoError(t, s.Add("projects/p/locations/us-west1"))
	assert.Equal(t, 2, s.Len())

	names, truncated := s.Names()
	assert.False(t, truncated)
	assert.Equal(t, []string{"projects/p/locations/europe-west2", "projects/p/locations/us-west1"}, names)

	got, err := s.Response(true)
	require.NoError(t, err)
	assert.Equal(t, names, got)

	got, err = s.Response(false)
	require.Nil(t, got)
	require.ErrorIs(t, err, unreachable.ErrUnreachable)
	require.ErrorIs(t, err, coreerrs.ErrUnavailable)

	e, ok := errors.AsType[*unreachable.Error](err)
	require.True(t, ok)
	assert.Equal(t, names, e.Names)
	assert.Contains(t, err.Error(), "2 resource(s) unreachable: projects/p/locations/europe-west2, projects/p/locations/us-west1")
}

func TestSetEmpty(t *testing.T) {
	t.Parallel()

	s := unreachable.NewSet()

	names, truncated := s.Names()
	assert.Nil(t, names)
	assert.False(t, truncated)

	for _, partial := range []bool{true, false} {
		got, err := s.Response(partial)
		require.NoError(t, err)
		assert.Nil(t, got)
	}
}

func TestSetAddIsAllOrNothing(t *testing.T) {
	t.Parallel()

	s := unreachable.NewSet()
	err := s.Add("projects/p/locations/a", "starwars4")
	require.ErrorIs(t, err, unreachable.ErrInvalidName)
	assert.Zero(t, s.Len())
}

func TestSetLimits(t *testing.T) {
	t.Parallel()

	zones := []string{
		"projects/p/locations/us-west1-a", "projects/p/locations/us-west1-b",
		"projects/p/locations/us-east1-b", "projects/p/locations/us-east1-c",
	}
	region := func(name string) string { return name[:strings.LastIndexByte(name, '-')] }

	tests := []struct {
		name      string
		opts      []unreachable.Option
		want      []string
		truncated bool
	}{
		{name: "within limit", opts: []unreachable.Option{unreachable.WithMaxNames(4)}, want: []string{
			"projects/p/locations/us-east1-b", "projects/p/locations/us-east1-c",
			"projects/p/locations/us-west1-a", "projects/p/locations/us-west1-b",
		}},
		{name: "truncated", opts: []unreachable.Option{unreachable.WithMaxNames(3)}, truncated: true, want: []string{
			"projects/p/locations/us-east1-b", "projects/p/locations/us-east1-c", "projects/p/locations/us-west1-a",
		}},
		{name: "upscoped", opts: []unreachable.Option{unreachable.WithMaxNames(2), unreachable.WithUpscope(region)},
			want: []string{"projects/p/locations/us-east1", "projects/p/locations/us-west1"}},
		{name: "upscoped and truncated",
			opts:      []unreachable.Option{unreachable.WithMaxNames(1), unreachable.WithUpscope(region)},
			truncated: true, want: []string{"projects/p/locations/us-east1"}},
		{name: "invalid upscope keeps the name",
			opts:      []unreachable.Option{unreachable.WithMaxNames(3), unreachable.WithUpscope(func(string) string { return "" })},
			truncated: true, want: []string{
				"projects/p/locations/us-east1-b", "projects/p/locations/us-east1-c", "projects/p/locations/us-west1-a",
			}},
		{name: "zero is unlimited", opts: []unreachable.Option{unreachable.WithMaxNames(0)}, want: []string{
			"projects/p/locations/us-east1-b", "projects/p/locations/us-east1-c",
			"projects/p/locations/us-west1-a", "projects/p/locations/us-west1-b",
		}},
		{name: "negative keeps the default", opts: []unreachable.Option{unreachable.WithMaxNames(-1)}, want: []string{
			"projects/p/locations/us-east1-b", "projects/p/locations/us-east1-c",
			"projects/p/locations/us-west1-a", "projects/p/locations/us-west1-b",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := unreachable.NewSet(tt.opts...)
			require.NoError(t, s.Add(zones...))

			names, truncated := s.Names()
			assert.Equal(t, tt.want, names)
			assert.Equal(t, tt.truncated, truncated)

			_, err := s.Response(false)
			e, ok := errors.AsType[*unreachable.Error](err)
			require.True(t, ok)
			assert.Equal(t, tt.truncated, e.Truncated)

			if tt.truncated {
				assert.True(t, strings.HasSuffix(err.Error(), ", ..."))
			}
		})
	}
}

func TestSetDefaultLimit(t *testing.T) {
	t.Parallel()

	s := unreachable.NewSet()
	for i := range unreachable.DefaultMaxNames + 1 {
		require.NoError(t, s.Add("projects/p/locations/l"+strings.Repeat("x", i)))
	}

	names, truncated := s.Names()
	assert.Len(t, names, unreachable.DefaultMaxNames)
	assert.True(t, truncated)
}

func TestSetConcurrentAdd(t *testing.T) {
	t.Parallel()

	s := unreachable.NewSet(unreachable.WithMaxNames(0))

	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			assert.NoError(t, s.Add("projects/p/locations/l"+strings.Repeat("x", i%8)))
		})
	}

	wg.Wait()
	assert.Equal(t, 8, s.Len())
}

func TestCheckGranularity(t *testing.T) {
	t.Parallel()

	require.NoError(t, unreachable.CheckGranularity(false, false))
	require.NoError(t, unreachable.CheckGranularity(false, true))
	require.NoError(t, unreachable.CheckGranularity(true, true))

	err := unreachable.CheckGranularity(true, false)
	require.ErrorIs(t, err, unreachable.ErrPartialSuccessUnsupported)
	require.ErrorIs(t, err, coreerrs.ErrInvalidArgument)

	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok)
	assert.Equal(t, unreachable.ReturnPartialSuccessField, fe.Field)
}

func TestErrorText(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "unreachable: 0 resource(s) unreachable", unreachable.NewError().Error())

	cause := errors.New("boom")
	err := &unreachable.Error{Names: []string{"a/b"}, Err: cause}
	require.ErrorIs(t, err, cause)
	require.ErrorIs(t, err, unreachable.ErrUnreachable)

	se := &unreachable.SourceError{Source: "a/b", Err: cause}
	require.ErrorIs(t, se, cause)
	assert.Equal(t, "unreachable: source a/b: boom", se.Error())
}
