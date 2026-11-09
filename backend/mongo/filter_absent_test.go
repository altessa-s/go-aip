// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// zeroFields declares one field of every kind as zero-when-absent.
func zeroFields() SchemaOption {
	return WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{
		"name":     filter.FieldKindString,
		"age":      filter.FieldKindInt,
		"price":    filter.FieldKindFloat,
		"active":   filter.FieldKindBool,
		"blob":     filter.FieldKindBytes,
		"at":       filter.FieldKindTimestamp,
		"row.name": filter.FieldKindString,
		"ignored":  filter.FieldKindUnspecified,
	})
}

func TestTranslator_ZeroWhenAbsent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, expr, want string
	}{
		// Comparisons: the absent branch follows the stored zero.
		{"equal zero string", `name == ""`, `{"$or":[{"name":""},{"name":null}]}`},
		{"not equal zero string", `name != ""`, `{"$and":[{"name":{"$ne":""}},{"name":{"$ne":null}}]}`},
		{"equal other string", `name == "x"`, `{"$and":[{"name":"x"},{"name":{"$ne":null}}]}`},
		{"not equal other string", `name != "x"`, `{"$or":[{"name":{"$ne":"x"}},{"name":null}]}`},
		{"string less than", `name < "a"`, `{"$or":[{"name":{"$lt":"a"}},{"name":null}]}`},
		{"string greater than", `name > "a"`, `{"$and":[{"name":{"$gt":"a"}},{"name":{"$ne":null}}]}`},
		{"equal zero int", `age == 0`, `{"$or":[{"age":0},{"age":null}]}`},
		{"int against float", `age < 0.5`, `{"$or":[{"age":{"$lt":0.5}},{"age":null}]}`},
		{"int less or equal negative", `age <= -1`, `{"$and":[{"age":{"$lte":-1}},{"age":{"$ne":null}}]}`},
		{"int greater or equal zero", `age >= 0`, `{"$or":[{"age":{"$gte":0}},{"age":null}]}`},
		{"unsigned zero", `age == 0u`, `{"$or":[{"age":0},{"age":null}]}`},
		{"float zero", `price == 0.0`, `{"$or":[{"price":0},{"price":null}]}`},
		{"bool false", `active == false`, `{"$or":[{"active":false},{"active":null}]}`},
		{"bool true", `active == true`, `{"$and":[{"active":true},{"active":{"$ne":null}}]}`},
		{"bool ordering", `active < true`, `{"$or":[{"active":{"$lt":true}},{"active":null}]}`},
		{"bytes empty", `blob == b""`, `{"$or":[{"blob":""},{"blob":null}]}`},

		// Incompatible literals behave as a stored zero does in MongoDB.
		{"string field equals number", `name == 0`, `{"$and":[{"name":0},{"name":{"$ne":null}}]}`},
		{"string field not equal number", `name != 0`, `{"$or":[{"name":{"$ne":0}},{"name":null}]}`},
		{"int field equals string", `age == ""`, `{"$and":[{"age":""},{"age":{"$ne":null}}]}`},
		{"int field ordered by string", `age < "a"`, `{"$and":[{"age":{"$lt":"a"}},{"age":{"$ne":null}}]}`},

		// null never equals a stored value.
		{"equal null", `name == null`, `{"$or":[{"name":""},{"name":null}]}`},
		{"not equal null", `name != null`, `{"$and":[{"name":{"$ne":""}},{"name":{"$ne":null}}]}`},

		// Membership.
		{"in with zero", `age in [0, 7]`, `{"$or":[{"age":{"$in":[0,7]}},{"age":null}]}`},
		{"in without zero", `age in [3, 7]`, `{"$and":[{"age":{"$in":[3,7]}},{"age":{"$ne":null}}]}`},
		{"in with mismatched zero", `name in [0]`, `{"$and":[{"name":{"$in":[0]}},{"name":{"$ne":null}}]}`},
		{"in with mixed list", `name in [0, ""]`, `{"$or":[{"name":{"$in":[0,""]}},{"name":null}]}`},
		{"in with null", `name in [null, "x"]`, `{"$and":[{"name":{"$in":[null,"x"]}},{"name":{"$ne":null}}]}`},

		// String predicates: only an empty needle matches "".
		{"contains empty", `name.contains("")`, `{"$or":[{"name":{"$regex":""}},{"name":null}]}`},
		{"contains other", `name.contains("a")`, `{"$and":[{"name":{"$regex":"a"}},{"name":{"$ne":null}}]}`},
		{"startsWith empty", `name.startsWith("")`, `{"$or":[{"name":{"$regex":"^"}},{"name":null}]}`},
		{"endsWith other", `name.endsWith("a")`, `{"$and":[{"name":{"$regex":"a\\z"}},{"name":{"$ne":null}}]}`},
		{"matches empty", `name.matches("^$")`, `{"$or":[{"name":{"$regex":"^\\z"}},{"name":null}]}`},
		{"matches non-empty", `name.matches("^.+$")`, `{"$and":[{"name":{"$regex":"^.+\\z"}},{"name":{"$ne":null}}]}`},
		{"regex on non-string", `age.contains("")`, `{"$and":[{"age":{"$regex":""}},{"age":{"$ne":null}}]}`},

		// has() is non-zero for declared scalar fields; bare and negated identifiers are exact.
		{"has", `has(row.name)`, `{"$and":[{"row.name":{"$ne":""}},{"row.name":{"$ne":null}}]}`},
		{"bare", `active`, `{"active":true}`},
		{"negated bare", `!active`, `{"active":{"$ne":true}}`},

		// Negation and nesting compose leaf by leaf.
		{
			"negated comparison", `!(name == "")`,
			`{"$nor":[{"$or":[{"name":""},{"name":null}]}]}`,
		},
		{
			"or of declared and plain", `active == false || status == "x"`,
			`{"$or":[{"$or":[{"active":false},{"active":null}]},{"status":"x"}]}`,
		},

		// Undeclared fields, and an Unspecified kind, are untouched.
		{"undeclared field", `status == ""`, `{"status":""}`},
		{"unspecified kind", `ignored == ""`, `{"ignored":""}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := mustTranslatorWithSchema(t, []SchemaOption{zeroFields()}).Translate(filtertest.MustParseFilter(t, tt.expr))
			require.NoError(t, err)
			require.JSONEq(t, tt.want, bsonToJSON(got))
		})
	}
}

// TestTranslator_ZeroWhenAbsentHas checks has() on a declared field: it
// needs a field selection to parse, so the declaration names the dotted path.
func TestTranslator_ZeroWhenAbsentHas(t *testing.T) {
	t.Parallel()
	trans := mustTranslatorWithSchema(t, []SchemaOption{WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"row.name": filter.FieldKindString})})

	got, err := trans.Translate(filtertest.MustParseFilter(t, `has(row.name)`))
	require.NoError(t, err)
	require.JSONEq(t, `{"$and":[{"row.name":{"$ne":""}},{"row.name":{"$ne":null}}]}`, bsonToJSON(got))

	got, err = trans.Translate(filtertest.MustParseFilter(t, `!has(row.name)`))
	require.NoError(t, err)
	require.JSONEq(t, `{"$nor":[{"$and":[{"row.name":{"$ne":""}},{"row.name":{"$ne":null}}]}]}`, bsonToJSON(got))
}

func TestTranslator_ZeroWhenAbsentSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, expr, wantBranch string
		zeroMatches            bool
	}{
		{"string size zero", `name.size() == 0`, "name", true},
		{"string size not zero", `name.size() != 0`, "name", false},
		{"string size below one", `name.size() < 1`, "name", true},
		{"string size above zero", `name.size() > 0`, "name", false},
		{"string size float", `name.size() <= 0.5`, "name", true},
		{"bytes size zero", `blob.size() == 0`, "blob", false},
		{"bytes size not zero", `blob.size() != 0`, "blob", true},
		{"bytes size below one", `blob.size() < 1`, "blob", false},
		{"int size not zero", `age.size() != 1`, "age", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := mustTranslatorWithSchema(t, []SchemaOption{zeroFields()}).Translate(filtertest.MustParseFilter(t, tt.expr))
			require.NoError(t, err)

			op, other := "$and", map[string]any{"$ne": nil}
			if tt.zeroMatches {
				op, other = "$or", nil
			}

			branches, ok := got[op].(bson.A)
			require.True(t, ok, "want %s, got %v", op, got)
			require.Len(t, branches, 2)

			if other == nil {
				require.Equal(t, bson.M{tt.wantBranch: nil}, branches[1])
			} else {
				require.Equal(t, bson.M{tt.wantBranch: bson.M(other)}, branches[1])
			}
		})
	}
}

func TestTranslator_ZeroWhenAbsentSizeRejectsNonNumbers(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		`name.size() < "x"`,
		`name.size() > null`,
		`!(name.size() < "x")`,
		`name.size() == true`,
	} {
		_, err := mustTranslatorWithSchema(t, []SchemaOption{zeroFields()}).Translate(filtertest.MustParseFilter(t, expr))
		require.ErrorIs(t, err, filter.ErrInvalidExpression, expr)
	}

	// Undeclared fields must also reject expression-like string operands.
	_, err := mustTranslator(t).Translate(filtertest.MustParseFilter(t, `name.size() < "x"`))
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
}

func TestTranslator_ZeroWhenAbsentTimestamp(t *testing.T) {
	t.Parallel()

	// The zero time is 0001-01-01T00:00:00Z. BSON dates hold milliseconds,
	// so a literal inside that millisecond equals it.
	tests := []struct {
		expr        string
		zeroMatches bool
	}{
		{`at == timestamp("0001-01-01T00:00:00Z")`, true},
		{`at == timestamp("0001-01-01T00:00:00.000999999Z")`, true},
		{`at == timestamp("0001-01-01T00:00:00.001Z")`, false},
		{`at < timestamp("0001-01-01T00:00:00.000999Z")`, false},
		{`at < timestamp("0001-01-01T00:00:00.001Z")`, true},
		{`at in [timestamp("0001-01-01T00:00:00.0005Z")]`, true},
		{`at > timestamp("2024-01-01T00:00:00Z")`, false},
	}

	for _, tt := range tests {
		got, err := mustTranslatorWithSchema(t, []SchemaOption{zeroFields()}).Translate(filtertest.MustParseFilter(t, tt.expr))
		require.NoError(t, err, tt.expr)

		_, isOr := got["$or"]
		require.Equal(t, tt.zeroMatches, isOr, tt.expr)
	}
}

func TestZeroMatchesComparison(t *testing.T) {
	t.Parallel()

	require.True(t, zeroMatchesComparison(filter.OpNotEqual, filter.FieldKindFloat, math.NaN()))
	require.False(t, zeroMatchesComparison(filter.OpEqual, filter.FieldKindFloat, math.NaN()))
	require.False(t, zeroMatchesComparison(filter.OpLT, filter.FieldKindFloat, math.NaN()))
	require.True(t, zeroMatchesComparison(filter.OpLT, filter.FieldKindInt, uint64(1)))
	require.True(t, zeroMatchesComparison(filter.OpEqual, filter.FieldKindBytes, []byte{}))
	require.False(t, zeroMatchesComparison(filter.OpEqual, filter.FieldKindBytes, []byte(nil)), "a nil slice is encoded as null")
	require.True(t, zeroMatchesComparison(filter.OpNotEqual, filter.FieldKindBytes, []byte(nil)))
	require.False(t, zeroMatchesIn(filter.FieldKindBytes, []any{[]byte(nil)}))
	require.True(t, zeroMatchesIn(filter.FieldKindBytes, []any{[]byte(nil), []byte{}}))
	require.True(t, zeroMatchesComparison(filter.OpGTE, filter.FieldKindTimestamp, time.Time{}))
	require.False(t, zeroMatchesComparison(filter.OpEqual, filter.FieldKindBool, int64(0)))
}

type emptyMatcher struct{}

func (emptyMatcher) MatchString(s string) bool { return s == "" }

func TestZeroWhenAbsentCustomRegex(t *testing.T) {
	t.Parallel()

	schema, err := NewSchema(zeroFields())
	require.NoError(t, err)
	tr, err := NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithRegexCompiler(func(string) (filter.RegexMatcher, error) { return emptyMatcher{}, nil }), filter.WithRegexValidator(func(string) error { return nil })), schema)
	require.NoError(t, err)
	got, err := tr.Translate(filtertest.MustParseFilter(t, `name.matches("(?<!a)$")`))
	require.NoError(t, err)
	require.Contains(t, got, "$or")
	// Literal predicates must not acquire the custom matcher's semantics.
	got, err = tr.Translate(filtertest.MustParseFilter(t, `name.contains("x")`))
	require.NoError(t, err)
	require.NotContains(t, got, "$or")
}
