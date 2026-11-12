// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func mustMongoEvaluator(tb testing.TB, opts ...any) *filtereval.Evaluator {
	tb.Helper()
	schema := mustMongoSchema(tb)
	eval, err := newEvaluator(append([]any{filtereval.WithProfile(schema)}, opts...)...)
	require.NoError(tb, err)

	return eval
}

// mongoRow is a document with every shape a field can take.
func mongoRow() map[string]any {
	return map[string]any{
		"name":    "ab",
		"empty":   "",
		"null":    nil,
		"age":     int64(30),
		"ratio":   1.5,
		"nan":     math.NaN(),
		"active":  true,
		"off":     false,
		"tags":    []any{"red", "green"},
		"nums":    []any{int64(3), int64(9)},
		"none":    []any{},
		"price":   decimal.RequireFromString("12.30"),
		"created": time.Date(2026, 1, 2, 3, 4, 5, 999_999_999, time.UTC),
		"address": map[string]any{"city": "NYC", "zip": nil},
		"steps": []any{
			map[string]any{"providerId": "a", "codes": []any{"x", "y"}},
			map[string]any{"providerId": "b"},
			map[string]any{"other": int64(1)},
		},
	}
}

func TestEvaluator_MongoSemantics(t *testing.T) {
	t.Parallel()

	eval := mustMongoEvaluator(t, withPolicy(filter.WithFieldTypes(map[string]filter.FieldKind{"price": filter.FieldKindDecimal})))

	tests := []struct {
		expr string
		want bool
	}{
		// Absent and null: a comparison is false, its negation true, and the rest of the expression still counts.
		{`missing == 1`, false},
		{`missing != 1`, true},
		{`missing < 1`, false},
		{`!(missing < 1)`, true},
		{`missing < 1 || age == 30`, true},
		{`missing == null`, true},
		{`age == null`, false},
		{`age != null`, true},
		// Values of another type compare as unequal and unordered.
		{`name == 1`, false},
		{`name != 1`, true},
		{`name > 1`, false},
		{`!(name > 1)`, true},
		{`age > "a"`, false},
		// Numbers compare by value across int, float and decimal.
		{`age == 30.0`, true},
		{`ratio < 2`, true},
		{`price == 12.3`, true},
		{`price > "12.29"`, true},
		{`nan == 1`, false},
		{`nan != 1`, true},
		{`nan < 1`, false},
		// Bools order false before true.
		{`active > false`, true},
		{`off < true`, true},
		{`off > false`, false},
		{`!(active > false)`, false},
		{`active`, true},
		{`off`, false},
		{`!off`, true},
		{`missing`, false},
		{`!missing`, true},
		// Timestamps compare at millisecond precision, the precision MongoDB stores.
		{`created == timestamp("2026-01-02T03:04:05.999Z")`, true},
		{`created > timestamp("2026-01-02T03:04:05.999Z")`, false},
		// A list matches when any element does; != when no element is equal.
		{`tags == "red"`, true},
		{`tags != "red"`, false},
		{`tags != "blue"`, true},
		{`none != "blue"`, true},
		{`none == "blue"`, false},
		{`nums > 5`, true},
		{`nums < 3`, false},
		{`tags in ["blue", "green"]`, true},
		{`!(tags in ["blue"])`, true},
		{`missing in [null]`, true},
		{`tags.startsWith("gr")`, true},
		{`contains(tags, "x")`, false},
		{`tags.matches("^re")`, true},
		{`age.contains("3")`, false},
		{`missing.startsWith("")`, false},
		// A path through a list of documents tests the values it resolves to, leaf lists flattened one level.
		{`steps.providerId == "b"`, true},
		{`steps.providerId != "c"`, true},
		{`steps.codes == "y"`, true},
		{`steps.missing == null`, true},
		{`address.city == "NYC"`, true},
		// has() reads $exists: a null value exists, a path through a list exists when any document has it.
		{`has(address.zip)`, true},
		{`has(address.street)`, false},
		{`has(steps.providerId)`, true},
		{`has(steps.nothing)`, false},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			t.Parallel()
			got, err := eval.Evaluate(filtertest.MustParseFilter(t, tt.expr), mongoRow())
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestEvaluator_MongoSemanticsDeclaredArrayNullAndHas(t *testing.T) {
	t.Parallel()

	eval := mustMongoEvaluator(t, withPolicy(filter.WithArrayFields("row.tags"), filter.WithPortableContract(true)))
	for _, tc := range []struct {
		expr string
		data map[string]any
		want bool
	}{
		{`row.tags == null`, map[string]any{"row": map[string]any{"tags": []any{}}}, true},
		{`row.tags != null`, map[string]any{"row": map[string]any{"tags": []any{}}}, false},
		{`has(row.tags)`, map[string]any{"row": map[string]any{"tags": []any{}}}, false},
		{`row.tags == null`, map[string]any{"row": map[string]any{"tags": nil}}, true},
		{`row.tags != null`, map[string]any{"row": map[string]any{"tags": nil}}, false},
		{`has(row.tags)`, map[string]any{"row": map[string]any{"tags": nil}}, false},
		{`row.tags == null`, map[string]any{"row": map[string]any{"tags": []any{"x"}}}, false},
		{`row.tags != null`, map[string]any{"row": map[string]any{"tags": []any{"x"}}}, true},
		{`has(row.tags)`, map[string]any{"row": map[string]any{"tags": []any{"x"}}}, true},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got, err := eval.Evaluate(filtertest.MustParseFilter(t, tc.expr), tc.data)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestEvaluator_MongoPresenceSemantics(t *testing.T) {
	t.Parallel()
	schema := mustMongoSchema(t, mongo.WithSchemaPresenceFields("rating", "score"))
	eval, err := newEvaluator(filtereval.WithProfile(schema), withPolicy(filter.WithAnyField(true)))
	require.NoError(t, err)

	for _, tc := range []struct {
		expr string
		data map[string]any
		want bool
	}{
		{`rating != 1`, map[string]any{"rating": nil}, false},
		{`rating == null`, map[string]any{"rating": nil}, true},
		{`rating != null`, map[string]any{"rating": nil}, false},
		{`!(rating > 1)`, map[string]any{"rating": nil}, false},
		{`!(rating in [1, 2])`, map[string]any{"rating": nil}, false},
		{`!(score.startsWith("a"))`, map[string]any{"score": nil}, false},
		{`rating != 1`, map[string]any{"rating": int64(2)}, true},
		{`!(rating > 1)`, map[string]any{"rating": int64(2)}, false},
		{`!(rating in [1, 2])`, map[string]any{"rating": int64(3)}, true},
		{`!(score.startsWith("a"))`, map[string]any{"score": "berry"}, true},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got, err := eval.Evaluate(filtertest.MustParseFilter(t, tc.expr), tc.data)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	got, err := eval.Evaluate(&filter.CallNode{Op: filter.OpHas, Target: &filter.IdentNode{Name: "score"}}, map[string]any{"score": nil})
	require.NoError(t, err)
	require.False(t, got)
}

func TestEvaluator_MongoSemanticsSize(t *testing.T) {
	t.Parallel()

	eval := mustMongoEvaluator(t, withPolicy(filter.WithArrayFields("list", "steps.providerId")))
	rows := map[string]map[string]any{
		"absent":     {},
		"null":       {"f": nil, "list": nil},
		"empty list": {"f": []any{}, "list": []any{}},
		"one":        {"f": []any{"x"}, "list": []any{"x"}},
		"empty str":  {"f": ""},
		"two runes":  {"f": "é😀"},
		"number":     {"f": int64(5)},
		"steps": {"steps": []any{
			map[string]any{"providerId": "a"}, map[string]any{"providerId": "b"}, map[string]any{"other": 1},
		}},
	}
	// The undeclared rows mirror the MongoDB translator: absent and null have no size under == and != and count as 0 when ordered.
	tests := []struct {
		expr string
		want []string
	}{
		{`f.size() == 0`, []string{"empty list", "empty str"}},
		{`f.size() != 0`, []string{"absent", "null", "one", "two runes", "number", "steps"}},
		{`f.size() == 2`, []string{"two runes"}},
		{`f.size() < 1`, []string{"absent", "null", "empty list", "empty str", "steps"}},
		{`f.size() > 0`, []string{"one", "two runes"}},
		{`!(f.size() > 0)`, []string{"absent", "null", "empty list", "empty str", "number", "steps"}},
		{`f.size() <= 0.5`, []string{"absent", "null", "empty list", "empty str", "steps"}},
		// A declared array has size 0 when absent or null, under every operator.
		{`list.size() == 0`, []string{"absent", "null", "empty list", "empty str", "two runes", "number", "steps"}},
		{`list.size() != 0`, []string{"one"}},
		{`list.size() >= 1`, []string{"one"}},
		{`steps.providerId.size() == 2`, []string{"steps"}},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			t.Parallel()

			var got []string

			for _, name := range []string{"absent", "null", "empty list", "one", "empty str", "two runes", "number", "steps"} {
				ok, err := eval.Evaluate(filtertest.MustParseFilter(t, tt.expr), rows[name])
				require.NoError(t, err, name)

				if ok {
					got = append(got, name)
				}
			}

			require.Equal(t, tt.want, got)
		})
	}
}

func TestEvaluator_MongoSemanticsZeroWhenAbsent(t *testing.T) {
	t.Parallel()

	semantics := mustMongoSchema(t, mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{
		"note": filter.FieldKindString, "meta.note": filter.FieldKindString, "retries": filter.FieldKindInt, "price": filter.FieldKindDecimal,
		"flag": filter.FieldKindBool, "at": filter.FieldKindTimestamp,
	}))
	eval := mustMongoEvaluator(t, filtereval.WithProfile(semantics))

	absent := map[string]any{}
	for expr, want := range map[string]bool{
		`note == ""`:                             true,
		`note != ""`:                             false,
		`note.startsWith("")`:                    true,
		`note.size() == 0`:                       true,
		`retries == 0`:                           true,
		`retries < 1`:                            true,
		`retries.size() == 0`:                    false,
		`retries.size() != 0`:                    true,
		`price == 0`:                             true,
		`flag == false`:                          true,
		`at < timestamp("2000-01-01T00:00:00Z")`: true,
		`has(meta.note)`:                         false,
		`note == null`:                           true,
		`note != null`:                           false,
	} {
		got, err := eval.Evaluate(filtertest.MustParseFilter(t, expr), absent)
		require.NoError(t, err, expr)
		require.Equal(t, want, got, expr)
	}

	// A stored null reads as the declared kind's zero value.
	got, err := eval.Evaluate(filtertest.MustParseFilter(t, `note == ""`), map[string]any{"note": nil})
	require.NoError(t, err)
	require.True(t, got)
}

// TestEvaluator_MongoSemanticsZeroWhenAbsentThroughLists pins the document-wide null guard,
// including empty traversals that matched the old $exists:false guard but do not match null.
func TestEvaluator_MongoSemanticsZeroWhenAbsentThroughLists(t *testing.T) {
	t.Parallel()

	semantics := mustMongoSchema(t, mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{
		"s.p": filter.FieldKindInt, "s.q.p": filter.FieldKindString,
	}))
	eval := mustMongoEvaluator(t, filtereval.WithProfile(semantics))
	empty := map[string]any{"s": []any{}}
	allLack := map[string]any{"s": []any{map[string]any{"x": int64(1)}, map[string]any{"q": []any{}}}}
	mixed := map[string]any{"s": []any{map[string]any{"p": int64(5)}, map[string]any{"x": int64(1)}}}
	mixedString := map[string]any{"s": []any{map[string]any{"q": []any{map[string]any{"p": "a"}, map[string]any{}}}}}

	tests := []struct {
		expr string
		data map[string]any
		want bool
	}{
		{`s.p == 0`, empty, false},
		{`s.p != 0`, empty, true},
		{`has(s.p)`, empty, true},
		{`s.p.size() != 0`, empty, true},
		{`s.q.p == ""`, empty, false},
		{`s.q.p.size() == 0`, empty, false},
		{`s.p == 0`, allLack, true},
		{`s.p < 1`, allLack, true},
		{`has(s.p)`, allLack, false},
		{`s.q.p.startsWith("")`, allLack, true},
		{`s.q.p.size() == 0`, allLack, true},
		// A missing candidate makes the whole predicate use the zero outcome.
		{`s.p == 0`, mixed, true},
		{`s.p != 0`, mixed, false},
		{`s.p == null`, mixed, true},
		{`s.p in [0, 5]`, mixed, true},
		{`s.p in [5]`, mixed, false},
		{`s.p > 0`, mixed, false},
		{`s.p == 5`, mixed, false},
		{`has(s.p)`, mixed, false},
		{`s.q.p == "a"`, mixedString, false},
		{`s.q.p == ""`, mixedString, true},
		{`s.q.p == null`, mixedString, true},
		{`s.q.p.startsWith("")`, mixedString, true},
		{`s.q.p.endsWith("a")`, mixedString, false},
		{`s.q.p.size() == 0`, mixedString, true},
		{`s.q.p.size() != 0`, mixedString, false},
		{`s.q.p.size() > 0`, mixedString, false},
		{`has(s.q.p)`, mixedString, false},
	}
	for _, tt := range tests {
		got, err := eval.Evaluate(filtertest.MustParseFilter(t, tt.expr), tt.data)
		require.NoError(t, err, tt.expr)
		require.Equal(t, tt.want, got, "%s on %v", tt.expr, tt.data)
	}
}

func TestEvaluator_MongoSemanticsErrors(t *testing.T) {
	t.Parallel()

	eval := mustMongoEvaluator(t, withPolicy(filter.WithAllowedFields("name", "tags", "age", "steps.providerId")),
		withPolicy(filter.WithFieldTypes(map[string]filter.FieldKind{"age": filter.FieldKindInt})))
	data := mongoRow()

	tests := []struct {
		expr string
		want error
	}{
		{`secret == 1`, filter.ErrFieldNotAllowed},
		{`has(secret.x)`, filter.ErrFieldNotAllowed},
		{`age == "x"`, filter.ErrFieldTypeMismatch},
		{`name.matches("(")`, filter.ErrInvalidRegex},
		{`tags.size() == -1`, filter.ErrInvalidExpression},
		{`tags.size() > 2147483648`, filter.ErrInvalidExpression},
		{`tags.size() != "2"`, filter.ErrInvalidExpression},
		{`name.contains(1)`, filter.ErrInvalidExpression},
		{`name in "x"`, filter.ErrInvalidExpression},
		// The shapes the MongoDB translator rejects: a literal on the left of a comparison with a field, and the expression forms it
		// cannot render (a computed substring() bound, a computed value against null, a computed string function argument).
		{`null == null`, filter.ErrInvalidExpression},
		{`2 == tags.size()`, filter.ErrInvalidExpression},
		{`"x" == name`, filter.ErrInvalidExpression},
		{`"red" in name`, filter.ErrInvalidExpression},
		{`name.substring(age) == "a"`, filter.ErrUnsupportedOperation},
		{`name.substring(0) == null`, filter.ErrUnsupportedOperation},
		{`tags.size()`, filter.ErrUnsupportedOperation},
		{`tags.size().size() == 1`, filter.ErrUnsupportedOperation},
		{`true`, filter.ErrInvalidExpression},
		{`name.contains(name.substring(0))`, filter.ErrUnsupportedOperation},
		{`name.contains(tags.size())`, filter.ErrUnsupportedOperation},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			t.Parallel()
			_, err := eval.Evaluate(filtertest.MustParseFilter(t, tt.expr), data)
			require.ErrorIs(t, err, tt.want)
		})
	}
}

// TestEvaluator_MongoSemanticsValidatesEveryBranch checks branches skipped by evaluation.
func TestEvaluator_MongoSemanticsValidatesEveryBranch(t *testing.T) {
	t.Parallel()

	eval := mustMongoEvaluator(t, withPolicy(filter.WithAllowedFields("active", "tags", "name")))
	for expr, want := range map[string]error{
		`active || tags.size() == -1`:    filter.ErrInvalidExpression,
		`!active && tags.size() > -2`:    filter.ErrInvalidExpression,
		`active || secret == 1`:          filter.ErrFieldNotAllowed,
		`active || name.matches("(")`:    filter.ErrInvalidRegex,
		`active || 1 == name`:            filter.ErrInvalidExpression,
		`active || name.contains(1)`:     filter.ErrInvalidExpression,
		`active || tags.size() == "one"`: filter.ErrInvalidExpression,
	} {
		node := filtertest.MustParseFilter(t, expr)
		for _, data := range []map[string]any{{"active": true}, {"active": false}, {}} {
			_, err := eval.Evaluate(node, data)
			require.ErrorIs(t, err, want, "%s on %v", expr, data)
		}

		require.ErrorIs(t, eval.Validate(node), want, expr)
		_, err := eval.Prepare(node)
		require.ErrorIs(t, err, want, expr)
	}

	_, err := eval.Evaluate(filtertest.MustParseFilter(t, `active || tags.size() == -1`), map[string]any{"active": true})
	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok, "an invalid size() operand is a field error")
	require.Equal(t, "tags", fe.Field)
}

func TestEvaluator_Prepare(t *testing.T) {
	t.Parallel()

	eval := mustMongoEvaluator(t)
	prepared, err := eval.Prepare(filtertest.MustParseFilter(t, `name == "ab" && tags.size() > 1`))
	require.NoError(t, err)
	got, err := prepared.Evaluate(mongoRow())
	require.NoError(t, err)
	require.True(t, got)
	got, err = prepared.Evaluate(map[string]any{"name": "ab"})
	require.NoError(t, err)
	require.False(t, got)

	all, err := eval.Prepare(nil)
	require.NoError(t, err)
	got, err = all.Evaluate(map[string]any{})
	require.NoError(t, err)
	require.True(t, got, "an absent filter matches every document, as the translator's empty query does")
}

// TestEvaluator_PrepareOwnsItsFilter pins that a Prepared evaluates the filter it validated: changing the caller's nodes or
// literal payloads after Prepare neither bypasses validation nor changes the result.
func TestEvaluator_PrepareOwnsItsFilter(t *testing.T) {
	t.Parallel()

	eval := mustMongoEvaluator(t, withPolicy(filter.WithAllowedFields("tags", "name", "b")))

	size := filtertest.MustParseFilter(t, `tags.size() == 1 && name == "ab"`)
	prepared, err := eval.Prepare(size)
	require.NoError(t, err)

	and, ok := size.(*filter.BinaryOpNode)
	require.True(t, ok)
	sizeCmp, ok := and.Left.(*filter.BinaryOpNode)
	require.True(t, ok)
	nameCmp, ok := and.Right.(*filter.BinaryOpNode)
	require.True(t, ok)
	sizeLit, ok := sizeCmp.Right.(*filter.LiteralNode)
	require.True(t, ok)

	sizeLit.Value = int64(-1) // a size() operand Validate rejects
	nameIdent, ok := nameCmp.Left.(*filter.IdentNode)
	require.True(t, ok)

	nameIdent.Name = "secret" // a field outside the allow-list
	nameCmp.Right = &filter.LiteralNode{Value: "zz"}

	require.ErrorIs(t, eval.Validate(size), filter.ErrInvalidExpression, "the mutated original is invalid")

	got, err := prepared.Evaluate(map[string]any{"tags": []any{"red"}, "name": "ab"})
	require.NoError(t, err)
	require.True(t, got, "the Prepared keeps the validated filter")

	payload := []byte("ab")
	bytesEq := &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "b"}, Right: &filter.LiteralNode{Value: payload}}
	preparedBytes, err := eval.Prepare(bytesEq)
	require.NoError(t, err)

	payload[0] = 'x'
	got, err = preparedBytes.Evaluate(map[string]any{"b": []byte("ab")})
	require.NoError(t, err)
	require.True(t, got, "a literal payload is copied")
}

// TestEvaluator_MongoSemanticsNullThroughLists pins MongoDB's reading of null through a list of documents: a document lacking the
// rest of the path is a null candidate, an empty list (or one without documents) has no candidate.
func TestEvaluator_MongoSemanticsNullThroughLists(t *testing.T) {
	t.Parallel()

	eval := mustMongoEvaluator(t)
	rows := []map[string]any{
		{"id": 1, "s": []any{}},
		{"id": 2, "s": []any{map[string]any{"x": 1}}},
		{"id": 3, "s": []any{map[string]any{"p": "a"}, map[string]any{"x": 1}}},
		{"id": 4, "s": []any{map[string]any{"p": "a"}}},
		{"id": 5},
		{"id": 6, "s": []any{map[string]any{"p": nil}}},
		{"id": 7, "s": []any{map[string]any{"p": "a"}, int64(5)}},
		{"id": 8, "s": []any{int64(5)}},
		{"id": 9, "s": int64(5)},
		{"id": 10, "s": []any{[]any{map[string]any{"p": "a"}}}},
		{"id": 11, "s": []any{map[string]any{"p": []any{"a"}}}},
		{"id": 12, "s": []any{map[string]any{"p": []any{}}}},
		{"id": 13, "s": map[string]any{"p": "a"}},
		{"id": 14, "s": []any{map[string]any{"p": []any{nil}}}},
	}
	// The expected ids are what MongoDB 8 returns for the translated queries.
	for expr, want := range map[string][]int{
		`s.p == null`:         {2, 3, 5, 6, 9, 14},
		`s.p != null`:         {1, 4, 7, 8, 10, 11, 12, 13},
		`s.p in [null]`:       {2, 3, 5, 6, 9, 14},
		`s.p in [null, "a"]`:  {2, 3, 4, 5, 6, 7, 9, 11, 13, 14},
		`!(s.p in [null])`:    {1, 4, 7, 8, 10, 11, 12, 13},
		`s.p == "a"`:          {3, 4, 7, 11, 13},
		`s.p != "a"`:          {1, 2, 5, 6, 8, 9, 10, 12, 14},
		`s.p.size() == 1`:     {11, 13, 14},
		`s.p.size() == 0`:     {12},
		`!(s.p.size() == 1)`:  {1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 12},
		`s.p.size() != 1`:     {1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 12},
		`s.p.size() > 0`:      {3, 4, 6, 7, 11, 12, 13, 14},
		`s.p.size() <= 0`:     {1, 2, 5, 8, 9, 10},
		`s.p.size() == 0.5`:   nil,
		`s.p.size() != 0.5`:   {1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14},
		`s.q.size() < 1`:      {1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14},
		`!(s.p.size() >= 1)`:  {1, 2, 5, 8, 9, 10},
		`has(s.p)`:            {3, 4, 6, 7, 11, 12, 13, 14},
		`s.p.startsWith("a")`: {3, 4, 7, 11, 13},
	} {
		node := filtertest.MustParseFilter(t, expr)

		var got []int

		for _, row := range rows {
			ok, err := eval.Evaluate(node, row)
			require.NoError(t, err, expr)

			if ok {
				got = append(got, row["id"].(int)) //nolint:forcetypeassert // fixture
			}
		}

		require.Equal(t, want, got, expr)
	}
}
