// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// mustTranslator builds a translator and fails the test on any
// construction error. Keeps the success-path tests free of
// error-wiring noise; tests that exercise construction failures call
// [NewFilterTranslator] directly.
func mustTranslator(tb testing.TB, opts ...any) *FilterTranslator {
	tb.Helper()

	policy, binding := anyField(tb, opts)
	tr, err := NewFilterTranslator(policy, binding...)
	require.NoError(tb, err)

	return tr
}

// anyField builds the policy of a test translator: filter.WithAnyField first (these tests pin translation itself, not the
// allow-list, so a test without WithAllowedFields translates any field; an allow-list given in opts still applies), then
// every filter.PolicyOption of opts. The filter.TranslatorOption values of opts bind the policy to storage names; FilterOption values
// of opts configure the translator.
func anyField(tb testing.TB, opts []any) (*filter.Policy, []FilterOption) {
	tb.Helper()

	rules := []filter.PolicyOption{filter.WithAnyField(true)}

	var (
		binding    []filter.TranslatorOption
		translator []FilterOption
	)

	for _, opt := range opts {
		switch opt := opt.(type) {
		case filter.PolicyOption:
			rules = append(rules, opt)
		case filter.TranslatorOption:
			binding = append(binding, opt)
		case FilterOption:
			translator = append(translator, opt)
		default:
			tb.Fatalf("unexpected test translator option %T", opt)
		}
	}

	return filtertest.MustPolicy(tb, rules...), append(translator, WithFilterOptions(binding...))
}

func TestFilterTranslator_BasicComparisons(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{"equal string", `name == "John"`, `name = "John"`},
		{"equal int", `age == 25`, `age = 25`},
		{"equal bool", `active == true`, `active = true`},
		{"not equal string", `status != "deleted"`, `status != "deleted"`},
		{"not equal int", `status != 0`, `status != 0`},
		{"greater than", `age > 18`, `age > 18`},
		{"greater than or equal", `age >= 18`, `age >= 18`},
		{"less than", `age < 65`, `age < 65`},
		{"less than or equal", `age <= 65`, `age <= 65`},
	}

	trans := mustTranslator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestFilterTranslator_LogicalOperators(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			"and",
			`name == "John" && age >= 18`,
			`(name = "John") AND (age >= 18)`,
		},
		{
			"or",
			`status == "active" || status == "pending"`,
			`(status = "active") OR (status = "pending")`,
		},
		{
			"not on bare ident",
			`!active`,
			`active = false`,
		},
		{
			"not on expression",
			`!(age >= 18)`,
			`age < 18`,
		},
		{
			"chained and",
			`a == 1 && b == 2 && c == 3`,
			`((a = 1) AND (b = 2)) AND (c = 3)`,
		},
	}

	trans := mustTranslator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestFilterTranslator_NestedFields(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{"two levels", `address.city == "NYC"`, `address.city = "NYC"`},
		{"three levels", `user.profile.name == "John"`, `user.profile.name = "John"`},
	}

	trans := mustTranslator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestFilterTranslator_InOperator(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			"string list",
			`status in ["active", "pending"]`,
			`status IN ["active", "pending"]`,
		},
		{
			"int list",
			`priority in [1, 2, 3]`,
			`priority IN [1, 2, 3]`,
		},
		{
			"single element",
			`type in [1]`,
			`type IN [1]`,
		},
	}

	trans := mustTranslator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestFilterTranslator_InOperator_ValueInField_Rejected(t *testing.T) {
	// `'value' in field` needs an array field declared with filter.WithArrayFields, as on the MongoDB translator.
	trans := mustTranslator(t)
	node := filtertest.MustParseFilter(t, `"units" in dictionaryCodes`)
	_, err := trans.Translate(node)
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
}

func TestFilterTranslator_StringFunctions(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{"contains", `name.contains("oh")`, `name CONTAINS "oh"`},
		{"startsWith", `name.startsWith("J")`, `name STARTS WITH "J"`},
	}

	trans := mustTranslator(t, WithFilterContainsFilter(true))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestFilterTranslator_UnsupportedStringFunctions(t *testing.T) {
	trans := mustTranslator(t)

	t.Run("endsWith", func(t *testing.T) {
		node := filtertest.MustParseFilter(t, `name.endsWith("x")`)
		_, err := trans.Translate(node)
		require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
	})

	t.Run("matches", func(t *testing.T) {
		node := filtertest.MustParseFilter(t, `name.matches("^[A-Z].*$")`)
		_, err := trans.Translate(node)
		require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
	})
}

func TestFilterTranslator_HasFunction(t *testing.T) {
	// CEL's `has()` macro requires a field-selection expression (dotted path),
	// not a bare identifier — that's a parser-level rule.
	trans := mustTranslator(t)

	node := filtertest.MustParseFilter(t, `has(user.email)`)
	got, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t, `user.email EXISTS`, got)
}

func TestFilterTranslator_SizeFunction_Unsupported(t *testing.T) {
	trans := mustTranslator(t)

	t.Run("size equality lhs", func(t *testing.T) {
		node := filtertest.MustParseFilter(t, `tags.size() == 3`)
		_, err := trans.Translate(node)
		require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
	})

	t.Run("size equality rhs", func(t *testing.T) {
		node := filtertest.MustParseFilter(t, `3 == tags.size()`)
		_, err := trans.Translate(node)
		require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
	})

	t.Run("size ordering rhs", func(t *testing.T) {
		node := filtertest.MustParseFilter(t, `5 < tags.size()`)
		_, err := trans.Translate(node)
		require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
	})

	t.Run("bare size call", func(t *testing.T) {
		node := filtertest.MustParseFilter(t, `size(tags)`)
		_, err := trans.Translate(node)
		require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
	})
}

func TestFilterTranslator_Timestamp(t *testing.T) {
	// timestamp() literals are emitted as Unix seconds — Meilisearch filters
	// numeric attributes only, and sub-second precision is dropped.
	trans := mustTranslator(t)

	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			"equality",
			`createdAt == timestamp("2024-01-02T03:04:05Z")`,
			`createdAt = 1704164645`,
		},
		{
			"greater than",
			`createdAt > timestamp("2024-01-02T03:04:05Z")`,
			`createdAt > 1704164645`,
		},
		{
			"sub-second precision is kept",
			`createdAt == timestamp("2024-01-02T03:04:05.789Z")`,
			`createdAt = 1704164645.789`,
		},
		{
			"nanoseconds without trailing zeros",
			`createdAt < timestamp("2024-01-02T03:04:05.000000120Z")`,
			`createdAt < 1704164645.00000012`,
		},
		{
			"before the epoch",
			`createdAt >= timestamp("1969-12-31T23:59:58.25Z")`,
			`createdAt >= -1.75`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestTranslator_NullValue pins both halves of the null question.
//
// Meilisearch splits what CEL's null conflates: an attribute can be
// absent from a document, or present and null. IS NULL alone answers
// only the second, so a filter built from it would miss every document
// that omits the attribute — and its negation would match all of them,
// which is exactly how a soft-delete filter leaks deleted documents.
func TestFilterTranslator_NullValue(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{"is null", `deleted_at == null`, `(deleted_at IS NULL OR deleted_at NOT EXISTS)`},
		{"is not null", `deleted_at != null`, `(deleted_at EXISTS AND deleted_at IS NOT NULL)`},
	}

	trans := mustTranslator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestFilterTranslator_NullValue_OrderingUnsupported(t *testing.T) {
	trans := mustTranslator(t)
	node := filtertest.MustParseFilter(t, `deleted_at > null`)
	_, err := trans.Translate(node)
	require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
}

func TestFilterTranslator_StringEscaping(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			"backslash",
			`name == "a\\b"`,
			`name = "a\\b"`,
		},
		{
			"quote",
			`name == "a\"b"`,
			`name = "a\"b"`,
		},
		{
			"both",
			`name == "a\\\"b"`,
			`name = "a\\\"b"`,
		},
	}

	trans := mustTranslator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestFilterTranslator_WithAllowedFields(t *testing.T) {
	trans := mustTranslator(t,
		filter.WithAllowedFields("name", "age"))

	t.Run("allowed", func(t *testing.T) {
		node := filtertest.MustParseFilter(t, `name == "John"`)
		_, err := trans.Translate(node)
		require.NoError(t, err)
	})

	t.Run("disallowed", func(t *testing.T) {
		node := filtertest.MustParseFilter(t, `email == "test@example.com"`)
		_, err := trans.Translate(node)
		require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
	})
}

func TestNewFilterTranslator_UntrustedInputRequiresAllowlist(t *testing.T) {
	_, err := filter.NewPolicy(filter.WithUntrustedInput(true), filter.WithAnyField(true))
	require.ErrorIs(t, err, filter.ErrAllowlistRequired)
}

func TestFilterTranslator_WithFieldMapping(t *testing.T) {
	trans := mustTranslator(t,
		filter.WithFieldMapping(map[string]string{
			"organizationIds": "organization_id",
			"dictionaryCodes": "dictionary_code",
		}))

	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			"mapped field equality",
			`organizationIds == "abc"`,
			`organization_id = "abc"`,
		},
		{
			"mapped field in list",
			`dictionaryCodes in ["currencies", "units"]`,
			`dictionary_code IN ["currencies", "units"]`,
		},
		{
			"unmapped field passes through",
			`status == 2`,
			`status = 2`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestFilterTranslator_WithMaxDepth(t *testing.T) {
	trans := mustTranslator(t,
		filter.WithMaxDepth(2))

	t.Run("within depth", func(t *testing.T) {
		node := filtertest.MustParseFilter(t, `name == "John" && age >= 18`)
		_, err := trans.Translate(node)
		require.NoError(t, err)
	})

	t.Run("exceeds depth", func(t *testing.T) {
		node := filtertest.MustParseFilter(t, `(name == "John" && age >= 18) && (status == "active" && type == "user")`)
		_, err := trans.Translate(node)
		require.ErrorIs(t, err, filter.ErrMaxDepthExceeded)
	})
}

func TestFilterTranslator_RealProtoCases(t *testing.T) {
	// Cases drawn from
	// proto/services/grpc/dictionaries/v1/search/dictionaries_search_service.proto.
	trans := mustTranslator(t,

		filter.WithAllowedFields("status", "type", "visibility", "organizationIds", "dictionaryCodes"),
		filter.WithFieldMapping(map[string]string{
			"organizationIds": "organization_id",
			"dictionaryCodes": "dictionary_code",
		}),
	)

	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			"single status",
			`status == 2`,
			`status = 2`,
		},
		{
			"type in list and visibility eq",
			`type in [1, 2] && visibility == 1`,
			`(type IN [1, 2]) AND (visibility = 1)`,
		},
		{
			"organizationIds membership via list shape",
			`organizationIds in ["org-uuid"]`,
			`organization_id IN ["org-uuid"]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestFilterTranslator_VisitMethodsUnexported pins that a FilterTranslator exposes no filter.Visitor methods: they mutate the per-call
// traversal state, so calling them on a shared translator would race with concurrent translations and skip the checks of Translate.
func TestFilterTranslator_VisitMethodsUnexported(t *testing.T) {
	t.Parallel()

	_, isVisitor := any(mustTranslator(t)).(filter.Visitor)
	require.False(t, isVisitor)
}

func TestQuoteString(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", `""`},
		{"hello", `"hello"`},
		{`a"b`, `"a\"b"`},
		{`a\b`, `"a\\b"`},
		{`a\"b`, `"a\\\"b"`},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			require.Equal(t, tt.want, quoteString(tt.in))
		})
	}
}

// TestQuoteLegacyString pins the quoting for Meilisearch 1.0 to 1.3 (SEC-12), whose filter parser unescapes only `\"`: a
// backslash is written as it is, and a string with an odd run of backslashes before a quote or at its end has no spelling.
func TestQuoteLegacyString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string // empty: rejected
	}{
		{"", `""`},
		{"hello", `"hello"`},
		{`a"b`, `"a\"b"`},
		{`a\b`, `"a\b"`},
		{`a\\b`, `"a\\b"`},
		{`a\\"b`, `"a\\\"b"`},
		{`a\\`, `"a\\"`},
		{`\n`, `"\n"`},
		{`a\"b`, ""},
		{`a\`, ""},
		{`a\\\`, ""},
		{`\\\"`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			got, err := quoteLegacyString(tt.in)
			if tt.want == "" {
				require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestFilterTranslator_BackslashByVersion pins SEC-12: Meilisearch 1.4 and later unescape `\\` in a quoted string, so a
// backslash is doubled there; before 1.4 it is written once, because the parser would read `\\` as two backslashes.
func TestFilterTranslator_BackslashByVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version Version
		expr    string
		want    string
		wantErr error
	}{
		{"latest", Version{}, `name == "a\\b"`, `name = "a\\b"`, nil},
		{"1.4", Version{Major: 1, Minor: 4}, `name == "a\\b\""`, `name = "a\\b\""`, nil},
		{"1.3", Version{Major: 1, Minor: 3}, `name == "a\\b"`, `name = "a\b"`, nil},
		{"1.3 quote", Version{Major: 1, Minor: 3}, `name == "say \"hi\""`, `name = "say \"hi\""`, nil},
		{"1.3 in", Version{Major: 1, Minor: 3}, `name in ["a\\b", "c"]`, `name IN ["a\b", "c"]`, nil},
		{"1.3 trailing", Version{Major: 1, Minor: 3}, `name == "a\\"`, "", filter.ErrUnsupportedOperation},
		{"1.3 before quote", Version{Major: 1, Minor: 3}, `name == "a\\\""`, "", filter.ErrUnsupportedOperation},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := mustTranslator(t, WithFilterServerVersion(tt.version)).Translate(filtertest.MustParseFilter(t, tt.expr))
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Empty(t, got)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestTranslator_BareIdentifierAtRoot pins the boolean-test rendering of
// a bare identifier used as a whole expression. acceptString has always
// handled it inside && and ||; at the root Translate visited the node
// directly and emitted the bare attribute name, which Meilisearch
// rejects as a missing operator.
func TestFilterTranslator_BareIdentifierAtRoot(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{"at the root", `active`, `active = true`},
		{"negated at the root", `!active`, `active = false`},
		{"as a conjunct", `active && verified`, `(active = true) AND (verified = true)`},
	}

	trans := mustTranslator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := filtertest.MustParseFilter(t, tt.expr)
			got, err := trans.Translate(node)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestTranslator_BareIdentifierRespectsPolicy guards that the root path
// goes through the allow-list and field mapping like every other field
// reference.
func TestFilterTranslator_BareIdentifierRespectsPolicy(t *testing.T) {
	t.Run("mapping applies", func(t *testing.T) {
		trans := mustTranslator(t, filter.WithFieldMapping(map[string]string{"isActive": "is_active"}))
		node := filtertest.MustParseFilter(t, `isActive`)

		got, err := trans.Translate(node)
		require.NoError(t, err)
		require.Equal(t, `is_active = true`, got)
	})

	t.Run("allow-list applies", func(t *testing.T) {
		trans := mustTranslator(t, filter.WithAllowedFields("name"))
		node := filtertest.MustParseFilter(t, `active`)

		_, err := trans.Translate(node)
		require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
	})
}

// TestTranslator_NullComposesUnderNegation checks that a negated null
// comparison renders the opposite parenthesized null form, which composes
// with its neighbors rather than only working standalone.
func TestFilterTranslator_NullComposesUnderNegation(t *testing.T) {
	trans := mustTranslator(t)
	node := filtertest.MustParseFilter(t, `!(deletedAt == null) && status == "x"`)

	got, err := trans.Translate(node)
	require.NoError(t, err)
	require.Equal(t,
		`((deletedAt EXISTS AND deletedAt IS NOT NULL)) AND (status = "x")`,
		got)
}

// TestTranslator_NilNodeMatchesAll pins the contract a caller with an
// optional filter depends on: an absent AST is the empty filter, which
// Meilisearch reads as no filtering, not a nil dereference.
func TestFilterTranslator_NilNodeMatchesAll(t *testing.T) {
	trans := mustTranslator(t)

	got, err := trans.Translate(nil)
	require.NoError(t, err)
	require.Empty(t, got)
}

// TestNewTranslator_RejectsDecimal: a FieldKindDecimal declaration fails construction instead of comparing through a float.
func TestNewFilterTranslator_RejectsDecimal(t *testing.T) {
	t.Parallel()

	_, err := NewFilterTranslator(filtertest.MustPolicy(t, filter.WithFieldTypes(map[string]filter.FieldKind{"price": filter.FieldKindDecimal}), filter.WithAnyField(true)))
	require.ErrorIs(t, err, filter.ErrUnsupportedType)
}
