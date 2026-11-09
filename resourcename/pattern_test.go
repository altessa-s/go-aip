// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package resourcename_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/resourcename"
)

func TestParsePatternValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern  string
		vars     []string
		segs     int
		singular bool
	}{
		{"publishers/{publisher}", []string{"publisher"}, 2, false},
		{"publishers/{publisher}/books/{book}", []string{"publisher", "book"}, 4, false},
		{"users/{user}/config", []string{"user"}, 3, true},
		{"users/{user}/settings/rules/{rule}", []string{"user", "rule"}, 5, false},
		{"config", nil, 1, true},
		{"projects/{project}/locations/{location}/v2Keys/{v2_key}", []string{"project", "location", "v2_key"}, 6, false},
		{"orgs/{org_unit9}", []string{"org_unit9"}, 2, false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			t.Parallel()

			p, err := resourcename.ParsePattern(tt.pattern)
			require.NoError(t, err)
			assert.Equal(t, tt.pattern, p.String())
			assert.Equal(t, tt.vars, p.Variables())
			assert.Equal(t, tt.segs, p.Segments())
			assert.Equal(t, tt.singular, p.Singleton())
		})
	}
}

func TestParsePatternInvalid(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"empty":                    "",
		"leading slash":            "/publishers/{publisher}",
		"trailing slash":           "publishers/{publisher}/",
		"double slash":             "publishers//{publisher}",
		"starts with variable":     "{publisher}/books",
		"consecutive variables":    "publishers/{publisher}/{book}",
		"upper-case collection":    "Publishers/{publisher}",
		"collection with dash":     "pub-lishers/{publisher}",
		"collection with under":    "pub_lishers/{publisher}",
		"collection starts digit":  "9books/{book}",
		"wildcard literal":         "publishers/-",
		"repeated collection":      "books/{book}/books/{other}",
		"unclosed variable":        "publishers/{publisher",
		"path template":            "publishers/{publisher=**}",
		"single letter variable":   "publishers/{p}",
		"camel variable":           "publishers/{pubLisher}",
		"variable trailing under":  "publishers/{publisher_}",
		"variable leading digit":   "publishers/{9publisher}",
		"variable with _id suffix": "publishers/{publisher_id}",
		"repeated variable":        "publishers/{book}/books/{book}",
		"empty variable":           "publishers/{}",
		"full name":                "//library.googleapis.com/publishers/{publisher}",
	}

	for name, pattern := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := resourcename.ParsePattern(pattern)
			require.ErrorIs(t, err, resourcename.ErrInvalidPattern)
		})
	}
}

func TestMustParsePatternPanics(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() { resourcename.MustParsePattern("bad/{X}") })
	assert.NotPanics(t, func() { resourcename.MustParsePattern("books/{book}") })
}

func TestPatternMatch(t *testing.T) {
	t.Parallel()

	p := resourcename.MustParsePattern("publishers/{publisher}/books/{book}")

	tests := []struct {
		name    string
		input   string
		want    map[string]string
		segment int
	}{
		{"match", "publishers/acme/books/les-miserables", map[string]string{"publisher": "acme", "book": "les-miserables"}, 0},
		{"wildcard is structural", "publishers/-/books/x", map[string]string{"publisher": "-", "book": "x"}, 0},
		{"too short", "publishers/acme", nil, -1},
		{"too long", "publishers/acme/books/x/editions/y", nil, -1},
		{"wrong literal", "publishers/acme/novels/x", nil, 2},
		{"wrong first literal", "authors/acme/books/x", nil, 0},
		{"empty id", "publishers//books/x", nil, 1},
		{"empty last id", "publishers/acme/books/", nil, 3},
		{"empty", "", nil, -1},
		{"leading slash", "/publishers/acme/books", nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := p.Match(tt.input)
			if tt.want != nil {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
				assert.True(t, p.Matches(tt.input))

				return
			}

			assert.False(t, p.Matches(tt.input))
			require.ErrorIs(t, err, resourcename.ErrNoMatch)
			require.ErrorIs(t, err, resourcename.ErrInvalidName)

			ne, ok := errors.AsType[*resourcename.NameError](err)
			require.True(t, ok)
			assert.Equal(t, tt.segment, ne.Segment)
			assert.NotEmpty(t, ne.Error())
		})
	}
}

func TestPatternAppendValues(t *testing.T) {
	t.Parallel()

	p := resourcename.MustParsePattern("publishers/{publisher}/books/{book}")
	dst := make([]string, 1, 4)
	dst[0] = "keep"

	got, err := p.AppendValues(dst, "publishers/acme/books/dune")
	require.NoError(t, err)
	assert.Equal(t, []string{"keep", "acme", "dune"}, got)

	got, err = p.AppendValues(dst, "publishers/acme")
	require.ErrorIs(t, err, resourcename.ErrNoMatch)
	assert.Equal(t, []string{"keep"}, got)
}

func TestPatternManyVariables(t *testing.T) {
	t.Parallel()

	p := resourcename.MustParsePattern("aa/{a1}/bb/{b1}/cc/{c1}/dd/{d1}/ee/{e1}/ff/{f1}/gg/{g1}/hh/{h1}/ii/{i1}")

	name := "aa/1/bb/2/cc/3/dd/4/ee/5/ff/6/gg/7/hh/8/ii/9"
	assert.True(t, p.Matches(name))

	vals, err := p.Match(name)
	require.NoError(t, err)
	assert.Len(t, vals, 9)
	assert.Equal(t, "9", vals["i1"])
}

func TestPatternSingletonMatch(t *testing.T) {
	t.Parallel()

	p := resourcename.MustParsePattern("users/{user}/config")

	vals, err := p.Match("users/vhugo1802/config")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"user": "vhugo1802"}, vals)

	_, err = p.Match("users/vhugo1802/settings")
	require.ErrorIs(t, err, resourcename.ErrNoMatch)
}

func TestPatternFormat(t *testing.T) {
	t.Parallel()

	p := resourcename.MustParsePattern("publishers/{publisher}/books/{book}")

	got, err := p.Format(map[string]string{"publisher": "acme", "book": "dune"})
	require.NoError(t, err)
	assert.Equal(t, "publishers/acme/books/dune", got)

	got, err = p.FormatValues("acme", "dune")
	require.NoError(t, err)
	assert.Equal(t, "publishers/acme/books/dune", got)

	single := resourcename.MustParsePattern("users/{user}/config")
	got, err = single.FormatValues("me")
	require.NoError(t, err)
	assert.Equal(t, "users/me/config", got)

	bad := []map[string]string{
		{"publisher": "acme"},
		{"publisher": "acme", "book": ""},
		{"publisher": "a/b", "book": "dune"},
		{"publisher": "acme", "book": "dune", "author": "x"},
	}
	for _, values := range bad {
		_, err = p.Format(values)
		require.ErrorIs(t, err, resourcename.ErrInvalidValue, "%v", values)
	}

	_, err = p.FormatValues("acme")
	require.ErrorIs(t, err, resourcename.ErrInvalidValue)
}

func TestPatternFormatRoundTrip(t *testing.T) {
	t.Parallel()

	p := resourcename.MustParsePattern("projects/{project}/locations/{location}/keys/{key}")
	name, err := p.FormatValues("p1", "us-east1", "k")
	require.NoError(t, err)

	vals, err := p.Match(name)
	require.NoError(t, err)

	again, err := p.Format(vals)
	require.NoError(t, err)
	assert.Equal(t, name, again)
}

func TestPatternParent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		parent  string
		ok      bool
	}{
		{"publishers/{publisher}/books/{book}", "publishers/{publisher}", true},
		{"publishers/{publisher}", "", false},
		{"users/{user}/config", "users/{user}", true},
		{"config", "", false},
		{"users/{user}/settings/rules/{rule}", "users/{user}/settings", true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			t.Parallel()

			pp, ok := resourcename.MustParsePattern(tt.pattern).Parent()
			require.Equal(t, tt.ok, ok)

			if ok {
				assert.Equal(t, tt.parent, pp.String())
				reparsed := resourcename.MustParsePattern(tt.parent)
				assert.Equal(t, reparsed.Variables(), pp.Variables())
			}
		})
	}
}

func TestParentPatternMatches(t *testing.T) {
	t.Parallel()

	pp, ok := resourcename.MustParsePattern("publishers/{publisher}/books/{book}").Parent()
	require.True(t, ok)

	vals, err := pp.Match("publishers/acme")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"publisher": "acme"}, vals)
}
