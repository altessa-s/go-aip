// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// parityOptions declare the array fields of parityRows: a top-level list (tags, nums) and dotted paths through a list of documents
// whose leaves are lists (steps.codes, steps.n) or scalars (steps.id). name and count are plain fields.
func parityOptions() []filter.PolicyOption {
	return []filter.PolicyOption{filter.WithArrayFields("tags", "nums", "steps.codes", "steps.id", "steps.n")}
}

// parityRow is a document of the default/MongoDB evaluator parity matrix. A uniform row holds values of one type per field; a
// mixed row also holds values of another type (nested lists, an integer among strings), which the default evaluator may refuse.
type parityRow struct {
	id      int
	uniform bool
	data    map[string]any
}

// parityRows are documents with every shape an array field can take: absent, null, empty, a scalar, a list with a null element, a
// path through a list of documents whose leaves are lists, missing, null or empty, non-document elements on the way, and nesting.
func parityRows() []parityRow {
	doc := func(kv ...any) map[string]any {
		m := make(map[string]any, len(kv)/2)
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1] //nolint:forcetypeassert // fixture keys
		}

		return m
	}
	list := func(v ...any) []any { return append([]any{}, v...) } // never nil, as the MongoDB matrix stores it

	return []parityRow{
		{1, true, doc()},
		{2, true, doc("tags", nil, "nums", nil, "steps", nil, "name", nil, "count", nil, "info", nil)},
		{3, true, doc("tags", list(), "nums", list(), "steps", list())},
		{4, true, doc("info", doc("name", "red"), "tags", list("red"), "nums", list(int64(5)), "name", "red", "count", int64(5),
			"steps", list(doc("codes", list("red"), "id", "red", "n", list(int64(5)))))},
		{5, true, doc("info", doc("name", nil), "tags", list("blue", "red"), "nums", list(int64(1), int64(9)), "name", "blue", "count", int64(9),
			"steps", list(doc("codes", list("blue"), "id", "blue", "n", list(int64(1))),
				doc("codes", list("green", "red"), "id", "red", "n", list(int64(9), int64(5)))))},
		{6, true, doc("tags", "red", "steps", list(doc("codes", "red", "id", "red", "n", int64(5))))},
		{7, true, doc("tags", list(nil, "red"), "nums", list(nil, int64(5)),
			"steps", list(doc("codes", list(nil, "red"), "id", nil, "n", list(nil, int64(5))), doc()))},
		{8, true, doc("tags", list("green"), "steps", list(doc("other", int64(1))))},
		{9, true, doc("steps", list(doc("codes", list(), "n", list()), doc("codes", nil, "id", nil, "n", nil)))},
		{10, true, doc("steps", list(doc("codes", list("red"), "id", "red", "n", list(int64(5))), "x", list("red"), nil))},
		{11, true, doc("steps", doc("codes", list("red"), "id", "red", "n", list(int64(5))))},
		{12, true, doc("steps", list(list(doc("codes", list("red"), "id", "red"))))},
		{13, false, doc("tags", list(list("red")), "nums", list(list(int64(5))), "steps", list(doc("codes", list(list("red")))))},
		{14, false, doc("tags", list("red", int64(1)), "nums", list(int64(5), "x"),
			"steps", list(doc("codes", list(int64(1), "red"), "id", int64(1), "n", list("x", int64(5)))))},
		{15, false, doc("nums", int64(5), "tags", true)},
		{16, false, doc("name", int64(1), "count", "x", "info", doc("name", int64(1)))},
	}
}

// parityExprs enumerates the predicates of the matrix: comparisons, membership both ways, null, negation, has(), size() and the
// string functions on every array field, and the same predicates on the plain fields where both evaluators accept them.
func parityExprs() []string {
	var exprs []string

	add := func(field string, text bool, array bool) {
		lit, other, low := `"red"`, `"blue"`, `"m"`
		if !text {
			lit, other, low = "5", "9", "2"
		}

		r := strings.NewReplacer("{F}", field, "{L}", lit, "{O}", other, "{W}", low)

		templates := []string{
			"{F} == {L}", "{F} != {L}", "{F} < {L}", "{F} > {W}", "{F} <= {L}", "{F} >= {L}", "!({F} == {L})", "!({F} > {W})", "!({F} != {L})",
			"{F} in [{L}, {O}]", "{F} in [null]", "!({F} in [{L}])", "{F} == null", "{F} != null", "!({F} == null)",
			"{F} > {W} && {F} < {L}", "{F} == {L} || {F} == {O}",
		}
		if strings.Contains(field, ".") { // has() takes a selection
			templates = append(templates, "has({F})", "!has({F})")
		}

		if array {
			templates = append(templates, "{L} in {F}", "!({L} in {F})", "null in {F}", "{F}.size() == 0", "{F}.size() == 1", "{F}.size() != 1",
				"{F}.size() > 1", "{F}.size() <= 1", "!({F}.size() < 1)")
		}

		if text {
			templates = append(templates, `{F}.contains("e")`, `{F}.startsWith("r")`, `{F}.endsWith("d")`, `{F}.matches("^r")`,
				`!{F}.contains("e")`, `startsWith({F}, "b")`)
		}

		for _, tpl := range templates {
			exprs = append(exprs, r.Replace(tpl))
		}
	}
	for _, f := range []string{"tags", "steps.codes", "steps.id"} {
		add(f, true, true)
	}

	for _, f := range []string{"nums", "steps.n"} {
		add(f, false, true)
	}

	add("name", true, false)
	add("count", false, false)
	add("info.name", true, false)

	return exprs
}

// TestEvaluator_ArrayFieldParity pins the contract of WithArrayFields across evaluator modes: on every predicate of the matrix the
// default evaluator selects the documents the MongoDB-semantics evaluator (and so the MongoDB translator) selects. It may refuse
// a document instead — with ErrUnsupportedType or ErrUnsupportedOperation, as documented — only when a value has another type, and
// select none where, on a plain field, an ordering comparison or size() meets an absent or null value: unknown, as in SQL, also
// under negation, where MongoDB's $not matches the document.
func TestEvaluator_ArrayFieldParity(t *testing.T) {
	t.Parallel()

	plain, err := newEvaluator(withPolicy(parityOptions()...))
	require.NoError(t, err)

	profiled := mustMongoEvaluator(t, withPolicy(parityOptions()...))
	for _, expr := range parityExprs() {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			node := filtertest.MustParseFilter(t, expr)
			arrayField := !strings.Contains(expr, "name") && !strings.Contains(expr, "count")

			for _, row := range parityRows() {
				want, err := profiled.Evaluate(node, row.data)
				require.NoError(t, err, "row %d", row.id)

				got, err := plain.Evaluate(node, row.data)
				if err != nil {
					require.True(t, !row.uniform || !arrayField, "row %d: %v", row.id, err)
					require.True(t, errors.Is(err, filter.ErrUnsupportedType) || errors.Is(err, filter.ErrUnsupportedOperation),
						"row %d: %v", row.id, err)

					continue
				}

				if !arrayField && want && !got {
					continue // an ordering comparison or size() of an absent or null plain field is unknown, also negated
				}

				require.Equal(t, want, got, "row %d", row.id)
			}
		})
	}
}

// TestEvaluator_ArrayFieldThroughDocuments: a declared array field reached through a list of documents whose leaves are lists
// tests the elements of those lists in both evaluator modes, as the MongoDB query does.
func TestEvaluator_ArrayFieldThroughDocuments(t *testing.T) {
	t.Parallel()

	data := map[string]any{"steps": []any{map[string]any{"codes": []any{"red"}}}}

	opts := []any{withPolicy(filter.WithArrayFields("steps.codes")), filtereval.WithProfile(mongo.DefaultSchema())}

	eval, err := newEvaluator(opts...)
	require.NoError(t, err)

	for expr, want := range map[string]bool{
		`steps.codes == "red"`: true, `"red" in steps.codes`: true, `steps.codes != "red"`: false,
		`steps.codes in ["red"]`: true, `steps.codes.startsWith("r")`: true, `steps.codes > "a"`: true,
		`steps.codes.size() == 1`: true, `has(steps.codes)`: true,
	} {
		got, err := eval.Evaluate(filtertest.MustParseFilter(t, expr), data)
		require.NoError(t, err, "%s", expr)
		require.Equal(t, want, got, "%s", expr)
	}
}
