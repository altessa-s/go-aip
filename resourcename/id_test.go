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

func TestPermissiveID(t *testing.T) {
	t.Parallel()

	valid := []string{"a", "les-miserables", "Upper", "0d8f5f4e-1b2c-4c3d-8e9f-0a1b2c3d4e5f", "user@example", "日本", "a b", "..."}
	for _, id := range valid {
		assert.NoError(t, resourcename.PermissiveID(id), "%q", id)
	}

	invalid := []string{"", "a/b", "-", ".", "..", "a\x00b", "a\nb", "a\x7fb", "\xff", "a\u0085b", "\xc3"}
	for _, id := range invalid {
		assert.Error(t, resourcename.PermissiveID(id), "%q", id)
	}
}

func TestStrictID(t *testing.T) {
	t.Parallel()

	valid := []string{"a", "les-miserables", "book1", "a-b-c", strings.Repeat("a", resourcename.MaxStrictIDLength)}
	for _, id := range valid {
		assert.NoError(t, resourcename.StrictID(id), "%q", id)
	}

	invalid := []string{
		"", "-", "1book", "Book", "book-", "book_1", "bo.ok", "日本",
		"0d8f5f4e-1b2c-4c3d-8e9f-0a1b2c3d4e5f", strings.Repeat("a", resourcename.MaxStrictIDLength+1),
	}
	for _, id := range invalid {
		assert.Error(t, resourcename.StrictID(id), "%q", id)
	}
}

func TestValidateUID(t *testing.T) {
	t.Parallel()

	valid := []string{"0d8f5f4e-1b2c-4c3d-8e9f-0a1b2c3d4e5f", "0D8F5F4E-1B2C-4C3D-AE9F-0A1B2C3D4E5F", "00000000-0000-4000-b000-000000000000"}
	for _, uid := range valid {
		assert.NoError(t, resourcename.ValidateUID(uid), uid)
	}

	invalid := []string{
		"",
		"0d8f5f4e-1b2c-1c3d-8e9f-0a1b2c3d4e5f",   // version 1
		"0d8f5f4e-1b2c-4c3d-ce9f-0a1b2c3d4e5f",   // variant
		"0d8f5f4e1b2c4c3d8e9f0a1b2c3d4e5f",       // no hyphens
		"0d8f5f4e-1b2c-4c3d-8e9f-0a1b2c3d4e5g",   // not hex
		"0d8f5f4e-1b2c-4c3d-8e9f_0a1b2c3d4e5f",   // separator
		"{0d8f5f4e-1b2c-4c3d-8e9f-0a1b2c3d4e5f}", // braces
	}
	for _, uid := range invalid {
		require.ErrorIs(t, resourcename.ValidateUID(uid), resourcename.ErrInvalidUID, uid)
	}
}
