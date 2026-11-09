// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/redis"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// TestEscapeTag pins S-23: every ASCII character but a letter, a digit and the underscore is escaped, so no value splits into
// terms (whitespace, a tab, a newline), widens into a fuzzy or prefix match (%, *) or reaches the query syntax. Letters, digits,
// the underscore and non-ASCII text pass unchanged.
func TestFilterEscapeTag(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, in, want string
	}{
		{"plain", "abc_XYZ09", "abc_XYZ09"},
		{"unicode", "Δίκτυο", "Δίκτυο"},
		{"space", "a b", `a\ b`},
		{"tab", "a\tb", "a\\\tb"},
		{"newline", "a\nb", "a\\\nb"},
		{"carriage return", "a\rb", "a\\\rb"},
		{"fuzzy", "%abc%", `\%abc\%`},
		{"prefix", "ab*", `ab\*`},
		{"field reference", "x)|(@other:{y}", `x\)\|\(\@other\:\{y\}`},
		{"punctuation", `<>#^&+=/?`, `\<\>\#\^\&\+\=\/\?`},
		{"backtick", "a`b", "a\\`b"},
		{"backslash", `a\b`, `a\\b`},
		{"NUL", "a\x00b", "a\\\x00b"},
		{"DEL", "a\x7fb", "a\\\x7fb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, redis.EscapeTag(tc.in))
		})
	}
}

// TestTranslate_EscapesSeparators pins that TAG and TEXT values are escaped with EscapeTag.
func TestFilterTranslate_EscapesSeparators(t *testing.T) {
	t.Parallel()

	tr, err := redis.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAllowedFields("tag", "text")), mustSchema(map[string]redis.FieldType{
		"tag": redis.FieldTypeTag, "text": redis.FieldTypeText,
	}))
	require.NoError(t, err)
	q, err := tr.Translate(filtertest.MustParseFilter(t, `tag == "a\tb%" && text == "c\nd%"`))
	require.NoError(t, err)
	require.Equal(t, "(@tag:{a\\\tb\\%} @text:(c\\\nd\\%))", q)
}
