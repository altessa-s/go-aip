// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// field is the expression of a field operand: missing and null read as null.
func field(name string) bson.M { return bson.M{"$ifNull": bson.A{"$" + name, nil}} }

// substr is the expression of name.substring(start, end).
func substr(name string, start, end int64) bson.M {
	s := field(name)

	return bson.M{"$cond": bson.A{
		bson.M{"$eq": bson.A{bson.M{"$type": s}, "string"}},
		bson.M{"$cond": bson.A{
			bson.M{"$gte": bson.A{bson.M{"$strLenCP": s}, end}},
			bson.M{"$substrCP": bson.A{s, start, end - start}},
			nil,
		}},
		nil,
	}}
}

func exprTranslator(t *testing.T, opts ...filter.PolicyOption) *mongo.FilterTranslator {
	t.Helper()

	opts = append([]filter.PolicyOption{filter.WithAnyField(true), filter.WithArrayFields("tags")}, opts...)
	tr, err := mongo.NewFilterTranslator(filtertest.MustPolicy(t, opts...))
	require.NoError(t, err)

	return tr
}

func TestExprComparisons(t *testing.T) {
	t.Parallel()

	a, b := field("a"), field("b")

	for _, tc := range []struct {
		expr string
		want bson.M
	}{
		{"a != b", bson.M{"$expr": bson.M{"$not": bson.A{bson.M{"$eq": bson.A{a, b}}}}}},
		{"a < b", bson.M{"$expr": bson.M{"$and": bson.A{
			bson.M{"$ne": bson.A{a, nil}},
			bson.M{"$ne": bson.A{b, nil}},
			bson.M{"$or": bson.A{
				bson.M{"$and": bson.A{bson.M{"$isNumber": a}, bson.M{"$isNumber": b}}},
				bson.M{"$eq": bson.A{bson.M{"$type": a}, bson.M{"$type": b}}},
			}},
			bson.M{"$lt": bson.A{a, b}},
		}}}},
		{"a in [b, 1, 2]", bson.M{"$or": bson.A{
			bson.M{"a": bson.M{"$in": []any{int64(1), int64(2)}}},
			bson.M{"$expr": bson.M{"$eq": bson.A{a, b}}},
		}}},
		{"a in [b]", bson.M{"$expr": bson.M{"$eq": bson.A{a, b}}}},
		{"a in tags", bson.M{"$expr": bson.M{"$or": bson.A{
			bson.M{"$eq": bson.A{field("tags"), a}},
			bson.M{"$cond": bson.A{bson.M{"$isArray": field("tags")}, bson.M{"$in": bson.A{a, field("tags")}}, false}},
		}}}},
		{"a.contains(b)", bson.M{"$expr": bson.M{"$cond": bson.A{
			bson.M{"$and": bson.A{bson.M{"$eq": bson.A{bson.M{"$type": a}, "string"}}, bson.M{"$eq": bson.A{bson.M{"$type": b}, "string"}}}},
			bson.M{"$ne": bson.A{bson.M{"$indexOfCP": bson.A{a, b}}, -1}},
			false,
		}}}},
		{"a.startsWith(b)", bson.M{"$expr": bson.M{"$cond": bson.A{
			bson.M{"$and": bson.A{bson.M{"$eq": bson.A{bson.M{"$type": a}, "string"}}, bson.M{"$eq": bson.A{bson.M{"$type": b}, "string"}}}},
			bson.M{"$eq": bson.A{bson.M{"$indexOfCP": bson.A{a, b}}, 0}},
			false,
		}}}},
		// A literal is wrapped in $literal: "$x" is never read as a field path.
		{`a.substring(1, 3) == "$x"`, bson.M{"$expr": bson.M{"$and": bson.A{
			bson.M{"$ne": bson.A{substr("a", 1, 3), nil}},
			bson.M{"$eq": bson.A{substr("a", 1, 3), bson.M{"$literal": "$x"}}},
		}}}},
		{`"ab" == a.substring(0, 2)`, bson.M{"$expr": bson.M{"$and": bson.A{
			bson.M{"$ne": bson.A{substr("a", 0, 2), nil}},
			bson.M{"$eq": bson.A{bson.M{"$literal": "ab"}, substr("a", 0, 2)}},
		}}}},
		// An empty list never matches; $or without terms is rejected by the server.
		{`a.substring(0, 1) in []`, bson.M{"$expr": false}},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			got, err := exprTranslator(t).Translate(filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestExprShapes(t *testing.T) {
	t.Parallel()

	tr := exprTranslator(t)

	for _, expr := range []string{
		"a.endsWith(b)", "a.substring(2) != b", "a.substring(0, 1) in [\"x\", b]", "a.substring(0, 1) in [\"x\"]",
		"tags.size() == n", "n > tags.size()", "a <= b || a >= b", "!(a > b)", "b in tags",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			node := filtertest.MustParseFilter(t, expr)
			got, err := tr.Translate(node)
			require.NoError(t, err)
			require.NotEmpty(t, got)

			ordered, err := tr.TranslateD(node)
			require.NoError(t, err)

			raw, err := bson.Marshal(ordered)
			require.NoError(t, err, "the ordered filter encodes as BSON")
			require.NotEmpty(t, raw)
			require.NoError(t, tr.ValidateAST(node))
		})
	}
}

func TestExprErrors(t *testing.T) {
	t.Parallel()

	opts := []filter.PolicyOption{filter.WithFieldTypes(map[string]filter.FieldKind{
		"name": filter.FieldKindString, "age": filter.FieldKindInt, "score": filter.FieldKindFloat,
	})}

	for _, tc := range []struct {
		expr string
		want error
	}{
		{"a.matches(b)", filter.ErrUnsupportedOperation},
		{"score < age", nil},
		{"name.substring(0, 1) == true", filter.ErrFieldTypeMismatch},
		{"name.substring(0, 1) == b\"x\"", filter.ErrFieldTypeMismatch},
		{"name.substring(0, 1) < 1.5", filter.ErrFieldTypeMismatch},
		{"name.substring(0, 1) == timestamp(\"2026-01-01T00:00:00Z\")", nil},
		{"name.substring(0, 1, 2) == \"x\"", filter.ErrInvalidExpression},
		{"name.substring(0, 1) != a.size()", filter.ErrFieldTypeMismatch},
		{"a.size() >= tags", filter.ErrUnsupportedOperation},
		{"tags == other", nil},
		{"a.contains(\"x\".size())", filter.ErrUnsupportedOperation},
		{"age == name", filter.ErrFieldTypeMismatch},
		{"age.substring(0, 1) == \"x\"", filter.ErrFieldTypeMismatch},
		{"name.substring(0, 1) == 5", filter.ErrFieldTypeMismatch},
		{"name.substring(0, 1) == null", filter.ErrUnsupportedOperation},
		{"name.substring(2, 1) == \"x\"", filter.ErrInvalidExpression},
		{"name.substring(-1) == \"x\"", filter.ErrInvalidExpression},
		{"name.substring(n) == \"x\"", filter.ErrUnsupportedOperation},
		{"name.substring(1.5) == \"x\"", filter.ErrInvalidExpression},
		{"tags.substring(0, 1) == \"x\"", filter.ErrUnsupportedOperation},
		{"tags < a", filter.ErrUnsupportedOperation},
		{"tags.contains(a)", filter.ErrUnsupportedOperation},
		{"a.contains(tags)", filter.ErrUnsupportedOperation},
		{"name.substring(0, 1)", filter.ErrUnsupportedOperation},
		{"name.substring(0, 1).contains(\"x\")", filter.ErrUnsupportedOperation},
		{"a.contains(b.substring(0, 1))", filter.ErrUnsupportedOperation},
		{"5 == tags.size()", filter.ErrInvalidExpression}, // a literal on the field side stays malformed
		{"tags.size() == \"x\" ", filter.ErrInvalidExpression},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			_, err := exprTranslator(t, opts...).Translate(filtertest.MustParseFilter(t, tc.expr))
			if tc.want == nil {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, tc.want)
		})
	}
}

// TestExprFieldAllowList: every field operand is authorized and mapped.
func TestExprFieldAllowList(t *testing.T) {
	t.Parallel()

	tr, err := mongo.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("a", "b")),
		mongo.WithFilterOptions(filter.WithFieldMapping(map[string]string{"b": "store_b"})))
	require.NoError(t, err)

	got, err := tr.Translate(filtertest.MustParseFilter(t, "a == b"))
	require.NoError(t, err)
	require.Equal(t, bson.M{"$expr": bson.M{"$eq": bson.A{field("a"), field("store_b")}}}, got)

	for _, expr := range []string{"a == secret", "a in [secret]", "a.contains(secret)", "secret.substring(0, 1) == \"x\""} {
		_, err = tr.Translate(filtertest.MustParseFilter(t, expr))
		require.ErrorIs(t, err, filter.ErrFieldNotAllowed, expr)
	}
}

// TestExprSchemaSemantics: a zero-when-absent field reads its zero; a presence field that is null makes the comparison false.
func TestExprSchemaSemantics(t *testing.T) {
	t.Parallel()

	schema, err := mongo.NewSchema(
		mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"z": filter.FieldKindString}),
		mongo.WithSchemaPresenceFields("p"),
	)
	require.NoError(t, err)

	tr, err := mongo.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), schema)
	require.NoError(t, err)

	zero := bson.M{"$ifNull": bson.A{"$z", ""}}

	got, err := tr.Translate(filtertest.MustParseFilter(t, "z == a"))
	require.NoError(t, err)
	require.Equal(t, bson.M{"$expr": bson.M{"$eq": bson.A{zero, field("a")}}}, got)

	got, err = tr.Translate(filtertest.MustParseFilter(t, "p != a"))
	require.NoError(t, err)
	require.Equal(t, bson.M{"$expr": bson.M{"$and": bson.A{
		bson.M{"$ne": bson.A{field("p"), nil}},
		bson.M{"$not": bson.A{bson.M{"$eq": bson.A{field("p"), field("a")}}}},
	}}}, got)

	// Negated membership with a field member: a null presence field matches neither form.
	got, err = tr.Translate(filtertest.MustParseFilter(t, "!(p in [a])"))
	require.NoError(t, err)
	require.Equal(t, bson.M{"$and": bson.A{
		bson.M{"p": bson.M{"$ne": nil}},
		bson.M{"$nor": bson.A{bson.M{"$expr": bson.M{"$and": bson.A{
			bson.M{"$ne": bson.A{field("p"), nil}},
			bson.M{"$eq": bson.A{field("p"), field("a")}},
		}}}}},
	}}, got)

	// A negated string function with a presence argument requires both strings.
	got, err = tr.Translate(filtertest.MustParseFilter(t, "!a.contains(p)"))
	require.NoError(t, err)

	and, ok := got["$and"].(bson.A)
	require.True(t, ok, "%v", got)
	require.Equal(t, bson.M{"a": bson.M{"$type": "string"}}, and[0])
	require.Equal(t, bson.M{"p": bson.M{"$type": "string"}}, and[1])
}
