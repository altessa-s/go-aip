// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/redis"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

var allowedFields = []string{"name", "age", "active", "tags", "score"}

var schema = map[string]redis.FieldType{
	"name":   redis.FieldTypeText,
	"age":    redis.FieldTypeNumeric,
	"active": redis.FieldTypeTag,
	"tags":   redis.FieldTypeTag,
	"score":  redis.FieldTypeNumeric,
}

// metacharacters are the RediSearch query-syntax characters a value must never
// carry unescaped. Transcribed from the dialect's own escaper: a value that
// slipped one through would stop being a term and start being syntax — `|` an
// alternation, `@` a field selector, `{}` a tag set, `*` a wildcard.
const metacharacters = `,.!{}()"-@:;[]'|~* `

var filterSeeds = []string{
	`name == "John"`,
	`age > 18`,
	`tags in ["a", "b"]`,
	`name.contains("oh")`,
	`active == true`,
	`name == "a|b"`,
	`name == "@admin"`,
	`name == "{x}"`,
	`name == "a b"`,
	`name == "*"`,
	`tags == "a,b"`,
	`unknown_field == "x"`,
	``,
}

func newTranslator(tb testing.TB) *redis.FilterTranslator {
	tb.Helper()

	tr, err := redis.NewFilterTranslatorWithSchema(filtertest.MustPolicy(tb, filter.WithUntrustedInput(true), filter.WithAllowedFields(allowedFields...)), mustSchema(schema))
	require.NoError(tb, err)

	return tr
}

// FuzzTranslateEscapesEveryMetacharacter is the injection oracle for
// RediSearch, whose query language has no quoting to hide inside: a value is
// escaped character by character, and anything missed is read as syntax.
//
// The consequence is not a crash but a silently different query. An unescaped
// `|` turns one term into an alternation and widens the result set; an
// unescaped `@` re-points the search at another field, which in a multi-tenant
// index is exactly the document set the filter was supposed to exclude.
func FuzzFilterTranslateEscapesEveryMetacharacter(f *testing.F) {
	for _, seed := range filterSeeds {
		f.Add(seed)
	}

	parser, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(f, err)

	f.Fuzz(func(t *testing.T, expression string) {
		node, err := parser.Parse(t.Context(), expression)
		if err != nil {
			return
		}

		query, err := newTranslator(t).Translate(node)
		if err != nil {
			return
		}

		// `|` separates the members of a tag set, so it is the translator's
		// own syntax between values and is checked separately below; inside a
		// single value every metacharacter must be escaped.
		hasList := containsList(node)

		for _, set := range tagSets(query) {
			values := splitUnescaped(set, '|')
			if !hasList {
				require.Len(t, values, 1,
					"an unescaped '|' split a single value into an alternation: %q in:\n%s", set, query)
			}

			for _, value := range values {
				for i := 0; i < len(value); i++ {
					if value[i] == '\\' {
						i++ // Escaped: skip whatever it protects.
						continue
					}

					require.NotContains(t, metacharacters, string(value[i]),
						"an unescaped %q reached a tag value %q in:\n%s", value[i], value, query)
				}
			}
		}
	})
}

// FuzzTranslateRejectsFieldsOutsideTheAllowlist states the allowlist property
// against the AST rather than the generated query, which has no unambiguous
// place a field name appears.
func FuzzFilterTranslateRejectsFieldsOutsideTheAllowlist(f *testing.F) {
	for _, seed := range filterSeeds {
		f.Add(seed)
	}

	f.Add(`secret == "x"`)
	f.Add(`password.contains("a")`)
	f.Add(`name == "x" && internal_flag == true`)

	parser, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(f, err)

	f.Fuzz(func(t *testing.T, expression string) {
		node, err := parser.Parse(t.Context(), expression)
		if err != nil {
			return
		}

		_, err = newTranslator(t).Translate(node)

		for _, field := range identifiers(node) {
			if !slices.Contains(allowedFields, field) {
				// Any rejection will do. Demanding ErrFieldNotAllowed
				// specifically would fire on expressions that are invalid for a
				// second reason too — `"0" && A` is refused as a bare literal
				// before the allow-list is ever consulted — and the property
				// here is that translation does not succeed.
				require.Error(t, err,
					"%q names %q, which the allowlist does not cover", expression, field)

				return
			}
		}
	})
}

// tagSets extracts the contents of every `{...}` tag set in a query — where
// caller-supplied values land. An unescaped brace inside one would end the set
// early, which is itself a failure the caller reports.
func tagSets(query string) []string {
	var (
		out     []string
		current []byte
		inSet   bool
	)

	for i := 0; i < len(query); i++ {
		switch {
		case query[i] == '\\' && i+1 < len(query):
			// An escaped brace is part of a value, not a delimiter — reading it
			// as one is how this scanner would invent an unescaped character
			// that is not there.
			if inSet {
				current = append(current, query[i], query[i+1])
			}

			i++
		case query[i] == '{':
			inSet, current = true, nil
		case query[i] == '}' && inSet:
			out = append(out, string(current))
			inSet, current = false, nil
		case inSet:
			current = append(current, query[i])
		}
	}

	return out
}

// splitUnescaped splits on a separator that is not preceded by a backslash, so
// an escaped occurrence stays part of its value.
func splitUnescaped(s string, sep byte) []string {
	var (
		out     []string
		current []byte
	)

	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s):
			current = append(current, s[i], s[i+1])
			i++
		case s[i] == sep:
			out = append(out, string(current))
			current = nil
		default:
			current = append(current, s[i])
		}
	}

	return append(out, string(current))
}

// containsList reports whether an expression has a list anywhere in it, which
// is what makes a tag set legitimately hold more than one value.
func containsList(node filter.Node) bool {
	if node == nil {
		return false
	}

	if _, ok := node.(*filter.ListNode); ok {
		return true
	}

	for child := range node.Children() {
		if containsList(child) {
			return true
		}
	}

	return false
}

// identifiers collects every field name an expression references, at any depth.
func identifiers(node filter.Node) []string {
	if node == nil {
		return nil
	}

	var out []string
	if ident, ok := node.(*filter.IdentNode); ok {
		out = append(out, ident.Name)
	}

	for child := range node.Children() {
		out = append(out, identifiers(child)...)
	}

	return out
}
