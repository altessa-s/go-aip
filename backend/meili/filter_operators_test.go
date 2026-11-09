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

// translateCase is one expression and either its rendering or the sentinel it fails with.
type translateCase struct {
	name string
	expr string
	want string
	err  error
}

func runCases(t *testing.T, trans *FilterTranslator, tests []translateCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := trans.Translate(filtertest.MustParseFilter(t, tt.expr))
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestFilterTranslator_ArrayMembership(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t, filter.WithArrayFields("tags", "codes"), filter.WithFieldTypes(map[string]filter.FieldKind{
		"tags": filter.FieldKindString, "codes": filter.FieldKindInt,
	}), filter.WithFieldMapping(map[string]string{"codes": "dictionary_codes"}))

	runCases(t, trans, []translateCase{
		{"string element", `"red" in tags`, `tags = "red"`, nil},
		{"int element", `3 in codes`, `dictionary_codes = 3`, nil},
		{"negated", `!("red" in tags)`, `NOT (tags = "red")`, nil},
		{"in a conjunction", `"red" in tags && 3 in codes`, `(tags = "red") AND (dictionary_codes = 3)`, nil},
		{"same as equality", `tags == "red"`, `tags = "red"`, nil},
		{"element kind mismatch", `"x" in codes`, "", filter.ErrFieldTypeMismatch},
		{"null element", `null in tags`, "", filter.ErrUnsupportedOperation},
		{"empty-string element", `"" in tags`, "", filter.ErrUnsupportedOperation},
		{"empty-string equality", `tags == ""`, "", filter.ErrUnsupportedOperation},
		{"field element", `name in tags`, "", filter.ErrUnsupportedOperation},
		{"undeclared array", `"x" in other`, "", filter.ErrInvalidExpression},
		{"in list with null", `tags in ["a", null]`, "", filter.ErrUnsupportedOperation},
		{"in list with empty string", `tags in ["a", ""]`, "", filter.ErrUnsupportedOperation},
		{"in list", `tags in ["a", "b"]`, `tags IN ["a", "b"]`, nil},
	})
}

func TestFilterTranslator_ArrayMembershipRespectsPolicy(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t, filter.WithArrayFields("tags"), filter.WithAllowedFields("name"))
	_, err := trans.Translate(filtertest.MustParseFilter(t, `"red" in tags`))
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
}

func TestFilterTranslator_EmptyAndNull(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t)

	runCases(t, trans, []translateCase{
		{"equals empty string", `name == ""`, `name IS EMPTY`, nil},
		{"not equals empty string", `name != ""`, `NOT (name IS EMPTY)`, nil},
		{"empty bytes", `data == b""`, `data IS EMPTY`, nil},
		{"ordering with empty string", `name > ""`, `name > ""`, nil},
		{"in list with null", `name in ["a", null]`, `(name IN ["a"] OR (name IS NULL OR name NOT EXISTS))`, nil},
		{"in list with empty string", `name in ["", "a"]`, `(name IN ["a"] OR name IS EMPTY)`, nil},
		{"in list of null only", `name in [null]`, `(name IS NULL OR name NOT EXISTS)`, nil},
		{"in list of both", `name in [null, ""]`, `((name IS NULL OR name NOT EXISTS) OR name IS EMPTY)`, nil},
		{"empty in list", `name in []`, `name IN []`, nil},
	})
}

func TestFilterTranslator_SizeEmptiness(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t, filter.WithArrayFields("tags"))

	const (
		emptyArray = `(tags NOT EXISTS OR tags IS NULL OR tags IS EMPTY)`
		nonEmpty   = `(tags EXISTS AND tags IS NOT NULL AND tags IS NOT EMPTY)`
	)

	runCases(t, trans, []translateCase{
		{"array == 0", `tags.size() == 0`, emptyArray, nil},
		{"array function form", `size(tags) == 0`, emptyArray, nil},
		{"array <= 0", `tags.size() <= 0`, emptyArray, nil},
		{"array < 1", `tags.size() < 1`, emptyArray, nil},
		{"array != 0", `tags.size() != 0`, nonEmpty, nil},
		{"array > 0", `tags.size() > 0`, nonEmpty, nil},
		{"array >= 1", `tags.size() >= 1`, nonEmpty, nil},
		{"mirrored 0 < size", `0 < tags.size()`, nonEmpty, nil},
		{"mirrored 1 > size", `1 > tags.size()`, emptyArray, nil},
		{"mirrored 0 == size", `0 == tags.size()`, emptyArray, nil},
		{"string == 0", `name.size() == 0`, `name IS EMPTY`, nil},
		{"string > 0", `name.size() > 0`, `(name EXISTS AND name IS NOT NULL AND name IS NOT EMPTY)`, nil},
		{"negated", `!(name.size() == 0)`, `(name EXISTS AND name IS NOT NULL AND name IS NOT EMPTY)`, nil},
		{"float operand", `tags.size() < 0.5`, emptyArray, nil},
		{"counting", `tags.size() == 3`, "", filter.ErrUnsupportedOperation},
		{"counting ordered", `tags.size() > 1`, "", filter.ErrUnsupportedOperation},
		{"always true", `tags.size() >= 0`, "", filter.ErrUnsupportedOperation},
		{"fractional equality", `tags.size() == 0.5`, "", filter.ErrUnsupportedOperation},
		{"string operand", `tags.size() == "0"`, "", filter.ErrInvalidExpression},
		{"size against size", `tags.size() == name.size()`, "", filter.ErrUnsupportedOperation},
		{"field operand", `tags.size() == count`, "", filter.ErrUnsupportedOperation},
	})
}

// TestTranslator_Negation pins how ! is pushed down to the predicates: a negated predicate the evaluator finds unknown for a
// null or absent value renders its opposite, which Meilisearch finds false there, and one the evaluator finds false keeps
// NOT (…), which selects that value.
func TestFilterTranslator_Negation(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t, filter.WithArrayFields("tags"))

	runCases(t, trans, []translateCase{
		{"less", `!(age < 30)`, `age >= 30`, nil},
		{"less or equal", `!(age <= 30)`, `age > 30`, nil},
		{"greater", `!(age > 30)`, `age <= 30`, nil},
		{"greater or equal", `!(age >= 30)`, `age < 30`, nil},
		{"equal", `!(name == "a")`, `name != "a"`, nil},
		{"not equal", `!(name != "a")`, `name = "a"`, nil},
		{"equal null", `!(name == null)`, `(name EXISTS AND name IS NOT NULL)`, nil},
		{"not equal null", `!(name != null)`, `(name IS NULL OR name NOT EXISTS)`, nil},
		{"ordered null", `!(age > null)`, "", filter.ErrUnsupportedOperation},
		{"empty string", `!(name == "")`, `NOT (name IS EMPTY)`, nil},
		{"bare bool", `!active`, `active = false`, nil},
		{"double", `!!(age < 30)`, `age < 30`, nil},
		{"triple", `!!!(age < 30)`, `age >= 30`, nil},
		{"de morgan and", `!(age < 30 && name == "a")`, `(age >= 30) OR (name != "a")`, nil},
		{"de morgan or", `!(age < 30 || active)`, `(age >= 30) AND (active = false)`, nil},
		{"nested", `!(age < 30 && !(name == "a" || active))`, `(age >= 30) OR ((name = "a") OR (active = true))`, nil},
		{"in", `!(age in [1, 2])`, `NOT (age IN [1, 2])`, nil},
		{"has", `!has(user.email)`, `NOT (user.email EXISTS)`, nil},
		{"starts with", `!name.startsWith("a")`, `NOT (name STARTS WITH "a")`, nil},
		{"array ordered", `!(tags < "c")`, `NOT (tags < "c")`, nil},
		{"array equal", `!(tags == "red")`, `tags != "red"`, nil},
		{"array not equal", `!(tags != "red")`, `tags = "red"`, nil},
		{"array element", `!("red" in tags)`, `NOT (tags = "red")`, nil},
		{"array size", `!(tags.size() == 0)`, `(tags EXISTS AND tags IS NOT NULL AND tags IS NOT EMPTY)`, nil},
		{"array size non-empty", `!(tags.size() > 0)`, `(tags NOT EXISTS OR tags IS NULL OR tags IS EMPTY)`, nil},
		{"string size non-empty", `!(name.size() > 0)`, `name IS EMPTY`, nil},
		{"field operands", `!(name < nickname)`, "", filter.ErrUnsupportedOperation},
	})
}

// TestTranslator_NegationBeforeNullEmpty checks that a negated ordering needs no IS NULL, so it translates for a server
// older than 1.2, while a negated emptiness test still needs it.
func TestFilterTranslator_NegationBeforeNullEmpty(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t, WithFilterServerVersion(Version{Major: 1, Minor: 1}))

	runCases(t, trans, []translateCase{
		{"ordering", `!(age < 30 || active)`, `(age >= 30) AND (active = false)`, nil},
		{"emptiness", `!(name.size() == 0)`, "", filter.ErrUnsupportedOperation},
		{"null", `!(name == null)`, "", filter.ErrUnsupportedOperation},
	})
}

func TestFilterTranslator_NegationDepth(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t, filter.WithMaxDepth(3))
	_, err := trans.Translate(filtertest.MustParseFilter(t, `!(a < 1 && (b < 1 || (c < 1 && d < 1)))`))
	require.ErrorIs(t, err, filter.ErrMaxDepthExceeded)
}

func TestFilterTranslator_SizeEmptinessRespectsPolicy(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t, filter.WithAllowedFields("name"))
	_, err := trans.Translate(filtertest.MustParseFilter(t, `tags.size() == 0`))
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
}

func TestFilterTranslator_ServerVersion(t *testing.T) {
	t.Parallel()

	type check struct {
		expr string
		want string // empty: fails with filter.ErrUnsupportedOperation
	}

	contains := check{`name.contains("a")`, `name CONTAINS "a"`}
	startsWith := check{`name.startsWith("a")`, `name STARTS WITH "a"`}
	isNull := check{`name == null`, `(name IS NULL OR name NOT EXISTS)`}
	notNull := check{`name != null`, `(name EXISTS AND name IS NOT NULL)`}
	isEmpty := check{`name == ""`, `name IS EMPTY`}
	sizeEmpty := check{`name.size() == 0`, `name IS EMPTY`}
	inNull := check{`name in [null]`, `(name IS NULL OR name NOT EXISTS)`}
	has := check{`has(user.email)`, `user.email EXISTS`}

	unsupported := func(c check) check { return check{expr: c.expr} }

	tests := []struct {
		name   string
		opts   []any
		checks []check
	}{
		{"latest by default", nil, []check{unsupported(contains), startsWith, isNull, notNull, isEmpty, sizeEmpty, has}},
		{"latest with containsFilter", []any{WithFilterContainsFilter(true)}, []check{contains, startsWith}},
		{"1.17", []any{WithFilterServerVersion(Version{Major: 1, Minor: 17})}, []check{unsupported(contains), startsWith}},
		{"2.0", []any{WithFilterServerVersion(Version{Major: 2})}, []check{startsWith, isNull}},
		{"1.16", []any{WithFilterServerVersion(Version{Major: 1, Minor: 16})}, []check{unsupported(contains), unsupported(startsWith)}},
		{"1.16 with containsFilter", []any{WithFilterServerVersion(Version{Major: 1, Minor: 16}), WithFilterContainsFilter(true)},
			[]check{contains, startsWith}},
		{"1.11 with containsFilter", []any{WithFilterServerVersion(Version{Major: 1, Minor: 11}), WithFilterContainsFilter(true)},
			[]check{contains, startsWith}},
		{"1.10 with containsFilter", []any{WithFilterServerVersion(Version{Major: 1, Minor: 10}), WithFilterContainsFilter(true)},
			[]check{contains, unsupported(startsWith)}},
		{"1.2", []any{WithFilterServerVersion(Version{Major: 1, Minor: 2})}, []check{isNull, notNull, isEmpty, sizeEmpty, inNull}},
		{"1.1", []any{WithFilterServerVersion(Version{Major: 1, Minor: 1})}, []check{
			unsupported(isNull), unsupported(notNull), unsupported(isEmpty), unsupported(sizeEmpty), unsupported(inNull), has,
			unsupported(startsWith),
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			trans := mustTranslator(t, tt.opts...)

			for _, c := range tt.checks {
				got, err := trans.Translate(filtertest.MustParseFilter(t, c.expr))
				if c.want == "" {
					require.ErrorIs(t, err, filter.ErrUnsupportedOperation, c.expr)
					continue
				}

				require.NoError(t, err, c.expr)
				require.Equal(t, c.want, got, c.expr)
			}
		})
	}
}

func TestNewFilterTranslator_InvalidServerVersion(t *testing.T) {
	t.Parallel()

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true))

	for name, opts := range map[string][]FilterOption{
		"before 1.0":                 {WithFilterServerVersion(Version{Minor: 29})},
		"negative minor":             {WithFilterServerVersion(Version{Major: 1, Minor: -1})},
		"containsFilter before 1.10": {WithFilterServerVersion(Version{Major: 1, Minor: 9}), WithFilterContainsFilter(true)},
		"negative major":             {WithFilterServerVersion(Version{Major: -1})},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := NewFilterTranslator(policy, opts...)
			require.ErrorIs(t, err, filter.ErrInvalidOptions)
		})
	}
}

func TestFilterTranslator_LiteralEncoderAppliesBeforeEmptiness(t *testing.T) {
	t.Parallel()

	// An encoder that maps the empty string to a stored sentinel keeps the plain equality.
	encoder := func(v any) (any, error) {
		if v == "" {
			return "UNSET", nil
		}

		return v, nil
	}

	trans := mustTranslator(t, WithFilterLiteralEncoder(encoder))

	runCases(t, trans, []translateCase{
		{"equality", `name == ""`, `name = "UNSET"`, nil},
		{"in list", `name in ["", "a"]`, `name IN ["UNSET", "a"]`, nil},
	})
}
