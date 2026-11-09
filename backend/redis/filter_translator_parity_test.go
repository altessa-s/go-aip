// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/redis"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/metrics"
)

// paritySchema is the storage schema of the parity cases.
var paritySchema = map[string]redis.FieldType{
	"name":         redis.FieldTypeText,
	"code":         redis.FieldTypeTag,
	"age":          redis.FieldTypeNumeric,
	"createdAt":    redis.FieldTypeNumeric,
	"tags":         redis.FieldTypeTag,
	"words":        redis.FieldTypeText,
	"scores":       redis.FieldTypeNumeric,
	"address.city": redis.FieldTypeTag,
	"data":         redis.FieldTypeTag,
}

// newParityTranslator builds a translator over paritySchema whose policy declares the field kinds and the array fields. opts
// holds redis.SchemaOption values for the schema and redis.FilterOption values for the translator.
func newParityTranslator(t *testing.T, opts ...any) *redis.FilterTranslator {
	t.Helper()

	policy := filtertest.MustPolicy(t,
		filter.WithAllowedFields("name", "code", "age", "createdAt", "tags", "words", "scores", "address.city", "data"),
		filter.WithFieldTypes(map[string]filter.FieldKind{
			"name":      filter.FieldKindString,
			"code":      filter.FieldKindString,
			"age":       filter.FieldKindInt,
			"createdAt": filter.FieldKindTimestamp,
			"tags":      filter.FieldKindString,
			"words":     filter.FieldKindString,
			"scores":    filter.FieldKindInt,
			"data":      filter.FieldKindBytes,
		}),
		filter.WithArrayFields("tags", "words", "scores"),
	)

	var (
		schemaOpts []redis.SchemaOption
		filterOpts []redis.FilterOption
	)

	for _, opt := range opts {
		switch o := opt.(type) {
		case redis.SchemaOption:
			schemaOpts = append(schemaOpts, o)
		case redis.FilterOption:
			filterOpts = append(filterOpts, o)
		default:
			t.Fatalf("unexpected option %T", opt)
		}
	}

	tr, err := redis.NewFilterTranslatorWithSchema(policy, mustSchema(paritySchema, schemaOpts...), filterOpts...)
	require.NoError(t, err)

	return tr
}

func TestFilterTranslator_NullComparison(t *testing.T) {
	t.Parallel()

	indexed := newParityTranslator(t, redis.WithSchemaIndexMissing("name", "code", "age", "address.city"))
	plain := newParityTranslator(t)

	t.Run("indexed", func(t *testing.T) {
		t.Parallel()

		for _, tt := range []struct{ expr, want string }{
			{`name == null`, `ismissing(@name)`},
			{`name != null`, `-ismissing(@name)`},
			{`null == code`, `ismissing(@code)`},
			{`null != age`, `-ismissing(@age)`},
			{`age != null && age > 3`, `(-ismissing(@age) @age:[(3 +inf])`},
			{`!(code == null)`, `-(ismissing(@code))`},
			{`has(address.city)`, `-ismissing(@address\.city)`},
			{`!has(address.city)`, `-(-ismissing(@address\.city))`},
			{`code in ["a", null]`, `((@code:{a})|(ismissing(@code)))`},
			{`code in [null]`, `ismissing(@code)`},
			{`!(code in ["a", null])`, `-(((@code:{a})|(ismissing(@code))))`},
			{`age in [1, null, 2]`, `(((@age:[1 1]|@age:[2 2]))|(ismissing(@age)))`},
		} {
			got, err := indexed.Translate(filtertest.MustParseFilter(t, tt.expr))
			require.NoError(t, err, tt.expr)
			assert.Equal(t, tt.want, got, tt.expr)
		}
	})

	// The literal tag "null" is an ordinary value: without INDEXMISSING a null comparison has no query form and must fail
	// instead of matching documents tagged "null" (or failing on the server for a NUMERIC field).
	t.Run("not indexed", func(t *testing.T) {
		t.Parallel()

		for _, expr := range []string{
			`name == null`, `code != null`, `age == null`, `null == code`, `has(address.city)`,
			`createdAt == null`, `code in ["a", null]`,
		} {
			_, err := plain.Translate(filtertest.MustParseFilter(t, expr))
			require.ErrorIs(t, err, filter.ErrUnsupportedOperation, expr)
		}
	})

	t.Run("rejected forms", func(t *testing.T) {
		t.Parallel()

		for _, expr := range []string{
			`age > null`,
			`null in tags`,
		} {
			_, err := indexed.Translate(filtertest.MustParseFilter(t, expr))
			require.ErrorIs(t, err, filter.ErrUnsupportedOperation, expr)
		}
	})

	t.Run("encoded null", func(t *testing.T) {
		t.Parallel()

		tr := newParityTranslator(t, redis.WithFilterLiteralEncoder(func(any) (any, error) { return nil, nil }))
		_, err := tr.Translate(filtertest.MustParseFilter(t, `code == "x"`))
		require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
	})
}

func TestFilterTranslator_ElementInArray(t *testing.T) {
	t.Parallel()

	tr := newParityTranslator(t)

	for _, tt := range []struct{ expr, want string }{
		{`"red" in tags`, `@tags:{red}`},
		{`"a b" in tags`, `@tags:{a\ b}`},
		{`"word" in words`, `@words:(word)`},
		{`3 in scores`, `@scores:[3 3]`},
		{`"red" in tags && !("blue" in tags)`, `(@tags:{red} -(@tags:{blue}))`},
	} {
		got, err := tr.Translate(filtertest.MustParseFilter(t, tt.expr))
		require.NoError(t, err, tt.expr)
		assert.Equal(t, tt.want, got, tt.expr)
	}

	_, err := tr.Translate(filtertest.MustParseFilter(t, `1 in tags`))
	require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)

	// A scalar field on the right is not a membership test.
	_, err = tr.Translate(filtertest.MustParseFilter(t, `"x" in name`))
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
}

func TestFilterTranslator_EndsWith(t *testing.T) {
	t.Parallel()

	tr := newParityTranslator(t)

	for _, tt := range []struct{ expr, want string }{
		{`name.endsWith("son")`, `@name:*son`},
		{`code.endsWith("-01")`, `@code:{*\-01}`},
		{`code.endsWith("a}|@x:{b")`, `@code:{*a\}\|\@x\:\{b}`},
	} {
		got, err := tr.Translate(filtertest.MustParseFilter(t, tt.expr))
		require.NoError(t, err, tt.expr)
		assert.Equal(t, tt.want, got, tt.expr)
	}

	for _, expr := range []string{`name.endsWith(code)`} {
		_, err := tr.Translate(filtertest.MustParseFilter(t, expr))
		require.ErrorIs(t, err, filter.ErrUnsupportedOperation, expr)
	}
}

func TestFilterTranslator_StringFunctionOnNumericField(t *testing.T) {
	t.Parallel()

	tr, err := redis.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAllowedFields("age")), mustSchema(map[string]redis.FieldType{"age": redis.FieldTypeNumeric}))
	require.NoError(t, err)

	for _, expr := range []string{`age.startsWith("1")`, `age.contains("1")`, `age.endsWith("1")`} {
		_, err := tr.Translate(filtertest.MustParseFilter(t, expr))
		require.ErrorIs(t, err, filter.ErrUnsupportedOperation, expr)
	}
}

func TestFilterTranslator_Timestamps(t *testing.T) {
	t.Parallel()

	tr := newParityTranslator(t)

	for _, tt := range []struct{ expr, want string }{
		{`createdAt > timestamp("2024-01-02T03:04:05Z")`, `@createdAt:[(1704164645 +inf]`},
		{`createdAt == timestamp("2024-01-02T03:04:05Z")`, `@createdAt:[1704164645 1704164645]`},
		{`createdAt >= timestamp("2024-01-02T03:04:05.5Z")`, `@createdAt:[1.7041646455e+09 +inf]`},
		{`createdAt in [timestamp("1970-01-01T00:00:10Z")]`, `(@createdAt:[10 10])`},
		{`createdAt < timestamp("1969-12-31T23:59:58.25Z")`, `@createdAt:[-inf (-1.75]`},
	} {
		got, err := tr.Translate(filtertest.MustParseFilter(t, tt.expr))
		require.NoError(t, err, tt.expr)
		assert.Equal(t, tt.want, got, tt.expr)
	}

	// A literal encoder chooses another unit.
	millis := newParityTranslator(t, redis.WithFilterLiteralEncoder(func(v any) (any, error) {
		if ts, ok := v.(time.Time); ok {
			return ts.UnixMilli(), nil
		}

		return v, nil
	}))
	got, err := millis.Translate(filtertest.MustParseFilter(t, `createdAt > timestamp("2024-01-02T03:04:05.5Z")`))
	require.NoError(t, err)
	assert.Equal(t, `@createdAt:[(1704164645500 +inf]`, got)
}

func TestFilterTranslator_Bytes(t *testing.T) {
	t.Parallel()

	tr := newParityTranslator(t)

	got, err := tr.Translate(filtertest.MustParseFilter(t, `data == b"a-b"`))
	require.NoError(t, err)
	assert.Equal(t, `@data:{a\-b}`, got)

	_, err = tr.Translate(filtertest.MustParseFilter(t, `data == b"\xff"`))
	require.ErrorIs(t, err, filter.ErrUnsupportedType)
}

func TestFilterTranslator_Options(t *testing.T) {
	t.Parallel()

	t.Run("strict schema", func(t *testing.T) {
		t.Parallel()

		tr := newParityTranslator(t, redis.WithSchemaStrict(true), redis.WithFilterCollector(metrics.Noop()))
		got, err := tr.Translate(filtertest.MustParseFilter(t, `code == "a"`))
		require.NoError(t, err)
		assert.Equal(t, `@code:{a}`, got)

		loose, err := redis.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), mustSchema(paritySchema, redis.WithSchemaStrict(true)))
		require.NoError(t, err)
		_, err = loose.Translate(filtertest.MustParseFilter(t, `other == "a"`))
		require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
	})

	t.Run("strict schema requires a schema", func(t *testing.T) {
		t.Parallel()

		for _, fields := range []map[string]redis.FieldType{nil, {}} {
			_, err := redis.NewSchema(fields, redis.WithSchemaStrict(true))
			require.ErrorIs(t, err, filter.ErrInvalidOptions)
		}

		_, err := redis.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), nil)
		require.ErrorIs(t, err, filter.ErrInvalidOptions, "nil schema")
	})

	t.Run("no schema falls back to TAG", func(t *testing.T) {
		t.Parallel()

		tr, err := redis.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
		require.NoError(t, err)
		got, err := tr.Translate(filtertest.MustParseFilter(t, `age == 5`))
		require.NoError(t, err)
		assert.Equal(t, `@age:{5}`, got)
	})

	t.Run("schema is copied", func(t *testing.T) {
		t.Parallel()

		schema := map[string]redis.FieldType{"age": redis.FieldTypeNumeric}
		tr, err := redis.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), mustSchema(schema))
		require.NoError(t, err)

		schema["age"] = redis.FieldTypeTag
		got, err := tr.Translate(filtertest.MustParseFilter(t, `age == 5`))
		require.NoError(t, err)
		assert.Equal(t, `@age:[5 5]`, got)
	})

	t.Run("predicate renderer", func(t *testing.T) {
		t.Parallel()

		tr := newParityTranslator(t, redis.WithFilterPredicateRenderer(func(n filter.Node) (string, bool, error) {
			if call, ok := n.(*filter.CallNode); ok && call.Op == filter.OpHas {
				return "@custom:{yes}", true, nil
			}

			return "", false, nil
		}))
		got, err := tr.Translate(filtertest.MustParseFilter(t, `has(address.city) && code == "a"`))
		require.NoError(t, err)
		assert.Equal(t, `(@custom:{yes} @code:{a})`, got)
	})

	t.Run("validate", func(t *testing.T) {
		t.Parallel()

		tr := newParityTranslator(t, redis.WithSchemaIndexMissing("name"))
		require.NoError(t, tr.ValidateAST(filtertest.MustParseFilter(t, `name == null`)))
		require.ErrorIs(t, tr.ValidateAST(filtertest.MustParseFilter(t, `code == null`)), filter.ErrUnsupportedOperation)
	})
}

func TestFilterTranslator_TextNotEqualAndEmptyIn(t *testing.T) {
	t.Parallel()

	tr := newParityTranslator(t)

	for _, tt := range []struct{ expr, want string }{
		{`name != "bob"`, `-@name:(bob)`},
		{`name in []`, `(@name:(0) -@name:(0))`},
		{`age in []`, `(@age:[0 0] -@age:[0 0])`},
		{`name in ["a", "b"]`, `@name:(a|b)`},
		{`age in [1, 2]`, `(@age:[1 1]|@age:[2 2])`},
	} {
		got, err := tr.Translate(filtertest.MustParseFilter(t, tt.expr))
		require.NoError(t, err, tt.expr)
		assert.Equal(t, tt.want, got, tt.expr)
	}
}

// The empty tag query @f:{} is a syntax error: an empty value has a query form only on a field declared INDEXEMPTY.
func TestFilterTranslator_EmptyValue(t *testing.T) {
	t.Parallel()

	t.Run("indexed", func(t *testing.T) {
		t.Parallel()

		tr := newParityTranslator(t, redis.WithSchemaIndexEmpty("code", "name"))

		for _, tt := range []struct{ expr, want string }{
			{`code == ""`, `@code:{""}`},
			{`code != ""`, `-@code:{""}`},
			{`name == ""`, `@name:""`},
			{`name != ""`, `-@name:""`},
			{`code in ["", "a"]`, `((@code:{a})|(@code:{""}))`},
			{`code in [""]`, `@code:{""}`},
			{`name in ["a", ""]`, `((@name:(a))|(@name:""))`},
			{`code == "a"`, `@code:{a}`},
		} {
			got, err := tr.Translate(filtertest.MustParseFilter(t, tt.expr))
			require.NoError(t, err, tt.expr)
			assert.Equal(t, tt.want, got, tt.expr)
		}
	})

	t.Run("not indexed", func(t *testing.T) {
		t.Parallel()

		tr := newParityTranslator(t)

		for _, expr := range []string{`code == ""`, `name != ""`, `code in ["a", ""]`, `"" in tags`, `data == b""`} {
			_, err := tr.Translate(filtertest.MustParseFilter(t, expr))
			require.ErrorIs(t, err, filter.ErrUnsupportedOperation, expr)
		}
	})
}

// Every string contains the empty string: the call is the presence test, never the invalid empty wildcard @f:{**}.
func TestFilterTranslator_EmptyNeedle(t *testing.T) {
	t.Parallel()

	indexed := newParityTranslator(t, redis.WithSchemaIndexMissing("code", "name"))
	plain := newParityTranslator(t)

	for _, tt := range []struct{ expr, want string }{
		{`code.contains("")`, `-ismissing(@code)`},
		{`code.startsWith("")`, `-ismissing(@code)`},
		{`name.endsWith("")`, `-ismissing(@name)`},
	} {
		got, err := indexed.Translate(filtertest.MustParseFilter(t, tt.expr))
		require.NoError(t, err, tt.expr)
		assert.Equal(t, tt.want, got, tt.expr)

		_, err = plain.Translate(filtertest.MustParseFilter(t, tt.expr))
		require.ErrorIs(t, err, filter.ErrUnsupportedOperation, tt.expr)
	}

	_, err := indexed.Translate(filtertest.MustParseFilter(t, `age.contains("")`))
	require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
}
