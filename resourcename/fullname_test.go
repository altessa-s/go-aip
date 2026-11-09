// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package resourcename_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/resourcename"
)

func TestParseFullName(t *testing.T) {
	t.Parallel()

	service, name, err := resourcename.ParseFullName("//library.googleapis.com/publishers/123/books/les-miserables")
	require.NoError(t, err)
	assert.Equal(t, "library.googleapis.com", service)
	assert.Equal(t, "publishers/123/books/les-miserables", name)

	service, name, err = resourcename.ParseFullName("//calendar.googleapis.com/users/vhugo1802")
	require.NoError(t, err)
	assert.Equal(t, "calendar.googleapis.com", service)
	assert.Equal(t, "users/vhugo1802", name)

	invalid := []string{
		"publishers/123",
		"https://library.googleapis.com/v1/publishers/123",
		"//library.googleapis.com",
		"//library.googleapis.com/",
		"///publishers/123",
		"//-bad.example.com/publishers/123",
		"//bad-.example.com/publishers/123",
		"//bad..example.com/publishers/123",
		"//bad_host.example.com/publishers/123",
		"//library.googleapis.com/publishers//books/x",
		"//" + strings.Repeat("a", 64) + ".com/publishers/1",
		"//" + strings.Repeat("a.", 127) + "com/publishers/1",
	}
	for _, full := range invalid {
		_, _, err := resourcename.ParseFullName(full)
		require.ErrorIs(t, err, resourcename.ErrInvalidFullName, full)
		require.ErrorIs(t, err, resourcename.ErrInvalidName, full)
	}
}

func TestFullName(t *testing.T) {
	t.Parallel()

	full, err := resourcename.FullName("library.googleapis.com", "publishers/123/books/les-miserables")
	require.NoError(t, err)
	assert.Equal(t, "//library.googleapis.com/publishers/123/books/les-miserables", full)
	assert.True(t, resourcename.IsFullName(full))
	assert.False(t, resourcename.IsFullName("publishers/123"))

	for _, c := range [][2]string{
		{"", "publishers/1"},
		{"bad host", "publishers/1"},
		{"library.googleapis.com", ""},
		{"library.googleapis.com", "//x.com/publishers/1"},
		{"library.googleapis.com", "publishers//1"},
	} {
		_, err := resourcename.FullName(c[0], c[1])
		require.ErrorIs(t, err, resourcename.ErrInvalidFullName, "%v", c)
	}
}
