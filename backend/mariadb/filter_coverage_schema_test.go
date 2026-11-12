// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func coverageSchema(tb testing.TB) *sqlbase.Schema {
	tb.Helper()

	schema, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"name":  sqlbase.Scalar("t.name", sqlbase.Text),
		"note":  sqlbase.PresenceScalar("note", sqlbase.Text),
		"n":     sqlbase.Scalar("n", sqlbase.Integer),
		"f":     sqlbase.Scalar("f", sqlbase.Float),
		"p":     sqlbase.Scalar("p", sqlbase.Numeric),
		"ok":    sqlbase.Scalar("ok", sqlbase.Boolean),
		"blob":  sqlbase.Scalar("blob", sqlbase.Bytes),
		"pblob": sqlbase.PresenceScalar("pblob", sqlbase.Bytes),
		"at":    sqlbase.Scalar("at", sqlbase.Timestamp),
		"doc":   sqlbase.Scalar("doc", sqlbase.JSON),
		"id":    sqlbase.Scalar("id", sqlbase.UUID),
		"jp":    sqlbase.JSONPath("doc", sqlbase.Text, "a"),
		"tags":  sqlbase.Array("tags", sqlbase.Text),
		"nums":  sqlbase.Array("nums", sqlbase.Integer),
		"flts":  sqlbase.Array("flts", sqlbase.Float),
		"blobs": sqlbase.Array("blobs", sqlbase.Bytes),
		"times": sqlbase.Array("times", sqlbase.Timestamp),
	})
	require.NoError(tb, err)

	return schema
}

// contractSchema returns the coverage schema in contract mode with opts.
func contractSchema(tb testing.TB, opts ...mariadb.SchemaOption) *mariadb.Schema {
	tb.Helper()

	s, err := mariadb.NewSchema(coverageSchema(tb), append([]mariadb.SchemaOption{mariadb.WithSchemaContractMode(true)}, opts...)...)
	require.NoError(tb, err)

	return s
}

func schemaTranslator(tb testing.TB, policyOpts []filter.PolicyOption, opts ...mariadb.SchemaOption) *mariadb.SchemaFilterTranslator {
	tb.Helper()

	policy := filtertest.MustPolicy(tb, append([]filter.PolicyOption{filter.WithAnyField(true)}, policyOpts...)...)
	tr, err := mariadb.NewFilterTranslatorWithSchema(policy, contractSchema(tb, opts...))
	require.NoError(tb, err)

	return tr
}

func TestSchemaConstructionOptions(t *testing.T) {
	t.Parallel()

	encoder := mariadb.WithFilterLiteralEncoder(func(v any) (any, error) { return v, nil })
	renderer := mariadb.WithFilterPredicateRenderer(func(filter.Node, sqlbase.ValueFunc) (string, bool, error) { return "", false, nil })

	for name, tc := range map[string]struct {
		opts []mariadb.FilterOption
		want error
	}{
		"literal encoder":        {[]mariadb.FilterOption{encoder}, filter.ErrInvalidOptions},
		"predicate renderer":     {[]mariadb.FilterOption{renderer}, filter.ErrInvalidOptions},
		"scalar decimal format":  {[]mariadb.FilterOption{mariadb.WithFilterDecimalFormat(sqlbase.DefaultDecimalFormat())}, filter.ErrInvalidOptions},
		"conflicting mapping":    {[]mariadb.FilterOption{mariadb.WithFilterOptions(filter.WithFieldMapping(map[string]string{"n": "other"}))}, filter.ErrInvalidOptions},
		"matching field mapping": {[]mariadb.FilterOption{mariadb.WithFilterOptions(filter.WithFieldMapping(map[string]string{"n": "n"}))}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tr, err := mariadb.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), contractSchema(t), tc.opts...)

			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				require.Nil(t, tr)

				return
			}

			require.NoError(t, err)
			require.NoError(t, tr.ValidateAST(filtertest.MustParseFilter(t, `n == 1`)))
		})
	}

	_, err := mariadb.NewFilterTranslatorWithSchema(nil, contractSchema(t))
	require.ErrorIs(t, err, filter.ErrPolicyRequired)

	_, err = mariadb.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithArrayFields("n")),
		contractSchema(t))
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
}

func TestSchemaOptions(t *testing.T) {
	t.Parallel()

	empty, blank, dotted := "", "  ", "a.b"

	for name, tc := range map[string]struct {
		opt  mariadb.SchemaOption
		want error
	}{
		"negative precision":   {mariadb.WithSchemaTimestampPrecision(-time.Millisecond), filter.ErrInvalidOptions},
		"dotted collation":     {mariadb.WithSchemaTextCollation("a.b"), filter.ErrInvalidOptions},
		"dotted collation ptr": {mariadb.WithSchemaTextCollation(&dotted), filter.ErrInvalidOptions},
		"invalid collation":    {mariadb.WithSchemaTextCollation("utf8mb4 bin"), filter.ErrInvalidOptions},
		"long collation":       {mariadb.WithSchemaTextCollation(strings.Repeat("c", sqlbase.MariaDBMaxIdentLen+1)), filter.ErrInvalidOptions},
		"dotted alias prefix":  {mariadb.WithSchemaArrayAliasPrefix("a.b"), filter.ErrInvalidOptions},
		"dotted alias pointer": {mariadb.WithSchemaArrayAliasPrefix(&dotted), filter.ErrInvalidOptions},
		"invalid alias prefix": {mariadb.WithSchemaArrayAliasPrefix("a-b"), filter.ErrInvalidOptions},
		"long alias prefix":    {mariadb.WithSchemaArrayAliasPrefix(strings.Repeat("a", sqlbase.MariaDBMaxIdentLen)), filter.ErrInvalidOptions},
		"invalid decimal":      {mariadb.WithSchemaDecimalFormat(sqlbase.DecimalFormat{Precision: 70}), filter.ErrInvalidOptions},
		"decimal scale":        {mariadb.WithSchemaDecimalFormat(sqlbase.DecimalFormat{Precision: 10, Scale: 11}), filter.ErrInvalidOptions},
		"zero precision":       {mariadb.WithSchemaTimestampPrecision(0), nil},
		"microseconds":         {mariadb.WithSchemaTimestampPrecision(time.Microsecond), nil},
		"nanoseconds":          {mariadb.WithSchemaTimestampPrecision(time.Nanosecond), nil},
		"custom collation":     {mariadb.WithSchemaTextCollation("utf8mb4_bin"), nil},
		"custom alias prefix":  {mariadb.WithSchemaArrayAliasPrefix("_el_"), nil},
		// Ignored values leave the defaults in place.
		"zero data budget":      {mariadb.WithSchemaMaxDataBudget(0), nil},
		"zero max value depth":  {mariadb.WithSchemaMaxValueDepth(0), nil},
		"zero max dimensions":   {mariadb.WithSchemaMaxArrayDimensions(-1), nil},
		"blank collation":       {mariadb.WithSchemaTextCollation(blank), nil},
		"nil collation pointer": {mariadb.WithSchemaTextCollation((*string)(nil)), nil},
		"empty alias pointer":   {mariadb.WithSchemaArrayAliasPrefix(&empty), nil},
		"nil alias pointer":     {mariadb.WithSchemaArrayAliasPrefix((*string)(nil)), nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s, err := mariadb.NewSchema(coverageSchema(t), mariadb.WithSchemaContractMode(true), tc.opt)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				require.Nil(t, s)

				return
			}

			require.NoError(t, err)
			tr, err := mariadb.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), s)
			require.NoError(t, err)
			require.NoError(t, tr.ValidateAST(filtertest.MustParseFilter(t, `n == 1`)))
		})
	}
}

func TestSchemaCollectorAndValidateAST(t *testing.T) {
	t.Parallel()

	meter := &countingMeter{}
	tr, err := mariadb.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), contractSchema(t),
		mariadb.WithFilterCollector(meter))
	require.NoError(t, err)

	require.NoError(t, tr.ValidateAST(filtertest.MustParseFilter(t, `name == "a"`)))
	require.ErrorIs(t, tr.ValidateAST(filtertest.MustParseFilter(t, `n == "a"`)), filter.ErrFieldTypeMismatch)

	_, _, err = tr.Translate(filtertest.MustParseFilter(t, `doc == "a"`))
	require.ErrorIs(t, err, filter.ErrUnsupportedOperation)

	assert.Equal(t, int64(3), meter.starts.Load())
	assert.Equal(t, meter.starts.Load(), meter.stops.Load())
	assert.Equal(t, "filter_mariadb", meter.subsystem)

	sql, args, err := tr.Translate(nil)
	require.NoError(t, err)
	assert.Equal(t, "1 = 1", sql)
	assert.Empty(t, args)
}

// TestSchemaAcceptedExpressions pins only behavior that is independent of NULL rendering: every placeholder is bound,
// and the bound values carry the expected Go types.
func TestSchemaAcceptedExpressions(t *testing.T) {
	t.Parallel()

	tr := schemaTranslator(t, nil)
	for _, tc := range []struct {
		expr string
		args []any
	}{
		{`n == 1`, []any{int64(1)}},
		{`n in [1, 2.5]`, []any{int64(1), 2.5}},
		{`n < 9223372036854775807`, []any{int64(9223372036854775807)}},
		{`f >= 1`, []any{int64(1)}},
		{`f < 1e308`, []any{1e308}},
		{`p > 1.25`, []any{"1.25"}},
		{`p in [1, 2]`, []any{"1", "2"}},
		{`ok == true`, []any{true}},
		{`ok`, nil},
		{`!ok`, nil},
		{`ok && n == 1 || name == "x"`, []any{int64(1), "x"}},
		{`blob == b"a"`, []any{[]byte("a")}},
		{`blobs == b"a"`, []any{`"YQ=="`}},
		{`blobs > b"a"`, []any{[]byte("a")}},
		{`flts <= 2.5`, []any{2.5}},
		{`flts == 2.5`, []any{"2.5"}},
		{`nums != 1`, []any{"1"}},
		{`nums in [1, 2]`, []any{"[1,2]"}},
		{`tags in ["a", "b"]`, []any{`["a","b"]`}},
		{`"a" in tags`, []any{`"a"`}},
		{`"a" == name`, []any{"a"}},
		{`size(name) == 1`, []any{int64(1)}},
		{`size(blob) > 1`, []any{int64(1)}},
		{`size(pblob) > 1`, []any{int64(1)}},
		{`size(tags) > 1`, []any{int64(1)}},
		{`size("abc") == 3`, []any{"abc", int64(3)}},
		{`size(b"ab") == 2`, []any{[]byte("ab"), int64(2)}},
		{`name in []`, nil},
		{`1 == 1`, []any{int64(1), int64(1)}},
		{`name.contains("a")`, []any{"a"}},
		{`note.endsWith("a")`, []any{"a", "a", "a"}},
		{`tags.endsWith("z")`, []any{"z", "z", "z"}},
		{`name.matches("\\d")`, []any{`\d`}},
		{`name.matches("(?i)é")`, []any{`(?i)é`}},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			node := filtertest.MustParseFilter(t, tc.expr)
			sql, args, err := tr.Translate(node)
			require.NoError(t, err)
			assert.Equal(t, tc.args, args)
			assert.Equal(t, len(args), strings.Count(sql, "?"), sql)
			require.NoError(t, tr.ValidateAST(node))
		})
	}
}

func TestSchemaRejectedExpressions(t *testing.T) {
	t.Parallel()

	tr := schemaTranslator(t, nil)
	for _, tc := range []struct {
		expr string
		want error
	}{
		{`doc == "a"`, filter.ErrUnsupportedOperation},
		{`id == "a"`, filter.ErrUnsupportedOperation},
		{`jp == "a"`, filter.ErrUnsupportedOperation},
		{`n == 1.5`, filter.ErrFieldTypeMismatch},
		{`n == "1"`, filter.ErrFieldTypeMismatch},
		{`n == 18446744073709551615u`, filter.ErrFieldTypeMismatch},
		{`f == "nan"`, filter.ErrFieldTypeMismatch},
		{`p == "nan"`, filter.ErrFieldTypeMismatch},
		{`p == "1e999999"`, filter.ErrFieldTypeMismatch},
		{`p == 1e40`, filter.ErrFieldTypeMismatch},
		{`at == "x"`, filter.ErrFieldTypeMismatch},
		{`ok == 1`, filter.ErrFieldTypeMismatch},
		{`blob == "a"`, filter.ErrFieldTypeMismatch},
		{`name == "a\x00"`, filter.ErrFieldTypeMismatch},
		{`name`, filter.ErrInvalidExpression},
		{`!name`, filter.ErrFieldTypeMismatch},
		{`ok && name`, filter.ErrFieldTypeMismatch},
		{`name || ok`, filter.ErrFieldTypeMismatch},
		{`size(n) == 1`, filter.ErrFieldTypeMismatch},
		{`size(ok) == 1`, filter.ErrFieldTypeMismatch},
		{`size([1, 2]) == 2`, filter.ErrUnsupportedOperation},
		{`name.contains(1)`, filter.ErrFieldTypeMismatch},
		{`n.contains("a")`, filter.ErrFieldTypeMismatch},
		{`name.contains(tags)`, filter.ErrFieldTypeMismatch},
		{`name.contains("a", "b")`, filter.ErrFieldTypeMismatch},
		{`name.matches(name)`, filter.ErrUnsupportedOperation},
		{`name.matches("(")`, filter.ErrInvalidRegex},
		{`n in [[1], 2]`, filter.ErrFieldTypeMismatch},
		{`n in [[1], [2, 3]]`, filter.ErrFieldTypeMismatch},
		{`n in [1, "a"]`, filter.ErrFieldTypeMismatch},
		{`n in [[1], [2]]`, filter.ErrUnsupportedOperation},
		{`name in name`, filter.ErrFieldTypeMismatch},
		{`name in tags`, filter.ErrUnsupportedOperation},
		{`tags in tags`, filter.ErrUnsupportedOperation},
		{`name == tags`, filter.ErrUnsupportedOperation},
		{`[1] == [1]`, filter.ErrUnsupportedOperation},
		{`n == name`, filter.ErrFieldTypeMismatch},
		{`nums == 1.5`, filter.ErrFieldTypeMismatch},
		{`has(name.x)`, filter.ErrInvalidOptions},
		{`missing == 1`, filter.ErrInvalidOptions},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			node := filtertest.MustParseFilter(t, tc.expr)
			_, _, err := tr.Translate(node)
			require.ErrorIs(t, err, tc.want)
			require.ErrorIs(t, tr.ValidateAST(node), tc.want)
		})
	}
}

func TestSchemaRejectedNodes(t *testing.T) {
	t.Parallel()

	tr := schemaTranslator(t, nil)
	ident := func(name string) *filter.IdentNode { return &filter.IdentNode{Name: name} }
	lit := func(v any) *filter.LiteralNode { return &filter.LiteralNode{Value: v} }

	for name, tc := range map[string]struct {
		node filter.Node
		want error
	}{
		"size of literal list arg": {&filter.BinaryOpNode{Op: filter.OpEqual,
			Left: &filter.CallNode{Op: filter.OpSize, Target: ident("name"), Args: []filter.Node{lit(int64(1))}}, Right: lit(int64(1))},
			filter.ErrFieldTypeMismatch},
		"has on literal":        {&filter.CallNode{Op: filter.OpHas, Target: lit("a")}, filter.ErrUnsupportedOperation},
		"has with argument":     {&filter.CallNode{Op: filter.OpHas, Target: ident("name"), Args: []filter.Node{lit("a")}}, filter.ErrUnsupportedOperation},
		"substring not boolean": {&filter.CallNode{Op: filter.OpSubstring, Target: ident("name"), Args: []filter.Node{lit(int64(1))}}, filter.ErrInvalidExpression},
		"string call no target": {&filter.CallNode{Op: filter.OpContains}, filter.ErrInvalidExpression},
		"unary size":            {&filter.UnaryOpNode{Op: filter.OpSize, Operand: ident("ok")}, filter.ErrUnsupportedOperation},
		"not of missing":        {&filter.UnaryOpNode{Op: filter.OpNot}, filter.ErrInvalidExpression},
		"binary missing right":  {&filter.BinaryOpNode{Op: filter.OpEqual, Left: ident("n")}, filter.ErrInvalidExpression},
		"binary contains op":    {&filter.BinaryOpNode{Op: filter.OpContains, Left: ident("name"), Right: lit("a")}, filter.ErrUnsupportedOperation},
		"opaque literal":        {&filter.BinaryOpNode{Op: filter.OpEqual, Left: ident("p"), Right: lit(struct{}{})}, filter.ErrFieldTypeMismatch},
		"text literal for int":  {&filter.BinaryOpNode{Op: filter.OpEqual, Left: ident("n"), Right: lit([]byte("a"))}, filter.ErrFieldTypeMismatch},
		"bool for timestamp":    {&filter.BinaryOpNode{Op: filter.OpEqual, Left: ident("at"), Right: lit(true)}, filter.ErrFieldTypeMismatch},
		"invalid utf-8":         {&filter.BinaryOpNode{Op: filter.OpEqual, Left: ident("name"), Right: lit("\xff")}, filter.ErrFieldTypeMismatch},
		"string call on number": {&filter.CallNode{Op: filter.OpStartsWith, Target: lit(int64(1)), Args: []filter.Node{lit("a")}}, filter.ErrFieldTypeMismatch},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, _, err := tr.Translate(tc.node)
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestSchemaLiteralTypes(t *testing.T) {
	t.Parallel()

	tr := schemaTranslator(t, nil)
	eq := func(field string, v any) filter.Node { return binary(filter.OpEqual, field, v) }
	i64 := int64(7)
	pi64 := &i64

	for name, tc := range map[string]struct {
		node filter.Node
		want error
	}{
		"int32 integer":              {eq("n", int32(7)), nil},
		"uint8 integer":              {eq("n", uint8(7)), nil},
		"int pointer numeric":        {eq("p", pi64), filter.ErrFieldTypeMismatch},
		"int double pointer numeric": {eq("p", &pi64), filter.ErrFieldTypeMismatch},
		"nil pointer numeric":        {eq("p", (*int64)(nil)), filter.ErrFieldTypeMismatch},
		"float32 float":              {eq("f", float32(1.5)), nil},
		"int float":                  {eq("f", int64(2)), nil},
		"int32 float":                {eq("f", int32(2)), nil},
		"json number float":          {eq("f", json.Number("2.5")), nil},
		"json nan float":             {eq("f", json.Number("NaN")), nil},
		"json inf float":             {eq("f", json.Number("Infinity")), nil},
		"json -inf float":            {eq("f", json.Number("-inf")), nil},
		"json overflow float":        {eq("f", json.Number("1e400")), filter.ErrFieldTypeMismatch},
		"json underflow float":       {eq("f", json.Number("1e-400")), filter.ErrFieldTypeMismatch},
		"json garbage float":         {eq("f", json.Number("x")), filter.ErrFieldTypeMismatch},
		"decimal numeric":            {eq("p", decimal.RequireFromString("1.5")), nil},
		"decimal huge exponent":      {eq("p", decimal.New(1, 200000)), filter.ErrFieldTypeMismatch},
		"decimal tiny exponent":      {eq("p", decimal.New(1, -20000)), filter.ErrFieldTypeMismatch},
		"json number numeric":        {eq("p", json.Number("3.25")), filter.ErrFieldTypeMismatch},
		"uint numeric":               {eq("p", uint64(3)), nil},
		"float numeric":              {eq("p", 2.5), nil},
		"int16 numeric":              {eq("p", int16(3)), filter.ErrFieldTypeMismatch},
		"uint16 numeric":             {eq("p", uint16(3)), filter.ErrFieldTypeMismatch},
		"float32 numeric":            {eq("p", float32(0.5)), filter.ErrFieldTypeMismatch},
		"too many digits numeric":    {eq("p", json.Number("1"+strings.Repeat("0", 140000))), filter.ErrFieldTypeMismatch},
		"too long numeric":           {eq("p", json.Number(strings.Repeat("1", 150000))), filter.ErrFieldTypeMismatch},
		"timestamp":                  {eq("at", time.Date(2026, 1, 2, 3, 4, 5, 999999999, time.UTC)), nil},
		"timestamp element":          {eq("times", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)), nil},
		"timestamp element order":    {binary(filter.OpGT, "times", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)), nil},
		"bytes":                      {eq("blob", []byte{0, 1}), nil},
		"nil bytes":                  {eq("blob", []byte(nil)), nil},
		"bool":                       {eq("ok", false), nil},
		"text":                       {eq("name", "x"), nil},
		"int64 above int range":      {eq("n", uint64(1<<63)), filter.ErrFieldTypeMismatch},
		"int8 integer":               {eq("n", int8(-3)), nil},
		"uint integer":               {eq("n", uint(3)), nil},
		"int integer":                {eq("n", 3), nil},
		"float64 text mismatch":      {eq("name", 1.5), filter.ErrFieldTypeMismatch},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sql, args, err := tr.Translate(tc.node)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				return
			}

			require.NoError(t, err)

			assert.Equal(t, len(args), strings.Count(sql, "?"), sql)
		})
	}
}

func TestSchemaLimits(t *testing.T) {
	t.Parallel()

	i64 := int64(1)
	p1 := &i64
	p2 := &p1

	for name, tc := range map[string]struct {
		opts []mariadb.SchemaOption
		node filter.Node
		want error
	}{
		"text budget":     {[]mariadb.SchemaOption{mariadb.WithSchemaMaxDataBudget(4)}, binary(filter.OpEqual, "name", "abcdefgh"), filter.ErrDataLimitExceeded},
		"bytes budget":    {[]mariadb.SchemaOption{mariadb.WithSchemaMaxDataBudget(4)}, binary(filter.OpEqual, "blob", []byte("abcdefgh")), filter.ErrDataLimitExceeded},
		"numeric budget":  {[]mariadb.SchemaOption{mariadb.WithSchemaMaxDataBudget(4)}, binary(filter.OpEqual, "p", "123456789"), filter.ErrDataLimitExceeded},
		"decimal budget":  {[]mariadb.SchemaOption{mariadb.WithSchemaMaxDataBudget(4)}, binary(filter.OpEqual, "p", decimal.New(123456789, 3)), filter.ErrDataLimitExceeded},
		"list budget":     {[]mariadb.SchemaOption{mariadb.WithSchemaMaxDataBudget(2)}, filtertest.MustParseFilter(t, `n in [1, 2, 3]`), filter.ErrDataLimitExceeded},
		"pointer depth":   {[]mariadb.SchemaOption{mariadb.WithSchemaMaxValueDepth(2)}, binary(filter.OpEqual, "p", p2), filter.ErrDataLimitExceeded},
		"array dims":      {[]mariadb.SchemaOption{mariadb.WithSchemaMaxArrayDimensions(1)}, filtertest.MustParseFilter(t, `n in [[1], [2]]`), filter.ErrFieldTypeMismatch},
		"budget suffices": {[]mariadb.SchemaOption{mariadb.WithSchemaMaxDataBudget(64)}, binary(filter.OpEqual, "name", "abc"), nil},
		"depth suffices":  {[]mariadb.SchemaOption{mariadb.WithSchemaMaxValueDepth(8)}, binary(filter.OpEqual, "n", int64(1)), nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, _, err := schemaTranslator(t, nil, tc.opts...).Translate(tc.node)
			if tc.want == nil {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestSchemaRenderingOptions(t *testing.T) {
	t.Parallel()

	tr := schemaTranslator(t, nil, mariadb.WithSchemaTextCollation("utf8mb4_bin"), mariadb.WithSchemaArrayAliasPrefix("_el_"),
		mariadb.WithSchemaTimestampPrecision(time.Second), mariadb.WithSchemaDecimalFormat(sqlbase.DecimalFormat{Precision: 20, Scale: 4}))

	sql, args, err := tr.Translate(filtertest.MustParseFilter(t, `tags.startsWith("a")`))
	require.NoError(t, err)
	assert.Contains(t, sql, "COLLATE utf8mb4_bin")
	assert.Contains(t, sql, "AS `_el_0`")
	assert.Equal(t, []any{"a"}, args)

	at := time.Date(2026, 1, 2, 3, 4, 5, 678000000, time.UTC)
	sql, args, err = tr.Translate(binary(filter.OpLT, "at", at))
	require.NoError(t, err)
	assert.Contains(t, sql, "DATETIME(0)")
	assert.Equal(t, []any{at.Truncate(time.Second)}, args)

	sql, args, err = tr.Translate(binary(filter.OpEqual, "times", at))
	require.NoError(t, err)
	assert.Contains(t, sql, "JSON_CONTAINS(`times`, ?)")
	assert.Equal(t, []any{`"2026-01-02T03:04:05Z"`}, args)

	sql, args, err = tr.Translate(binary(filter.OpGT, "times", at))
	require.NoError(t, err)
	assert.Contains(t, sql, "`element` DATETIME(0) PATH '$'")
	assert.Equal(t, []any{at.Truncate(time.Second)}, args)

	sql, args, err = tr.Translate(filtertest.MustParseFilter(t, `p >= 1.5`))
	require.NoError(t, err)
	assert.Contains(t, sql, "DECIMAL(20,4)")
	assert.Equal(t, []any{"1.5"}, args)

	_, _, err = tr.Translate(filtertest.MustParseFilter(t, `p >= 1.00001`))
	require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)

	// Each array predicate gets its own alias, skipping names already used by columns.
	sql, _, err = tr.Translate(filtertest.MustParseFilter(t, `nums > 1 && flts < 2.5 && blobs > b"a"`))
	require.NoError(t, err)
	assert.Contains(t, sql, "`_el_0`")
	assert.Contains(t, sql, "`_el_1`")
	assert.Contains(t, sql, "`_el_2`")
	assert.Contains(t, sql, "`element` DOUBLE PATH")
	assert.Contains(t, sql, "`element` LONGBLOB PATH")
}

func TestSchemaPresenceOperators(t *testing.T) {
	t.Parallel()

	tr := schemaTranslator(t, nil)

	// has() and exists() must accept every storage kind and bind every placeholder they emit; their exact SQL shape is
	// pinned by the contract tests.
	for _, field := range []string{"name", "note", "n", "f", "p", "ok", "blob", "pblob", "at", "tags", "nums", "times"} {
		for _, op := range []filter.Operator{filter.OpHas, filter.OpExists} {
			t.Run(field+"/"+op.String(), func(t *testing.T) {
				t.Parallel()

				sql, args, err := tr.Translate(&filter.CallNode{Op: op, Target: &filter.IdentNode{Name: field}})
				require.NoError(t, err)
				assert.Equal(t, len(args), strings.Count(sql, "?"), sql)
				assert.Contains(t, sql, "`"+field+"`")
			})
		}
	}

	for _, field := range []string{"doc", "id", "jp"} {
		_, _, err := tr.Translate(&filter.CallNode{Op: filter.OpHas, Target: &filter.IdentNode{Name: field}})
		require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
	}
}

func TestSchemaPortableContract(t *testing.T) {
	t.Parallel()

	tr := schemaTranslator(t, []filter.PolicyOption{filter.WithPortableContract(true)})

	for _, tc := range []struct {
		expr string
		want error
	}{
		{`p == 1.5`, nil},
		{`p in [1, 2.5]`, nil},
		{`nums > 1`, nil},
		{`name.contains("a")`, nil},
		{`p == "1e999999"`, filter.ErrFieldTypeMismatch},
		{`p == "x"`, filter.ErrFieldTypeMismatch},
		{`name.matches("\\d")`, filter.ErrUnsupportedOperation},
		{`name.matches("(?i)é")`, filter.ErrUnsupportedOperation},
		{`name.matches("(?i)abc")`, nil},
		{`name.matches("(")`, filter.ErrInvalidRegex},
		{`name.contains("a", "b")`, filter.ErrInvalidExpression},
		{`1 == 1`, filter.ErrInvalidExpression},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			sql, args, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, len(args), strings.Count(sql, "?"), sql)
		})
	}

	regex := schemaTranslator(t, []filter.PolicyOption{filter.WithContractRules(map[filter.ContractRule]bool{filter.PortableRegex: true})})
	for _, tc := range []struct {
		expr string
		want error
	}{
		{`name.matches("^a.*b$")`, nil},
		{`name.matches("(?i)abc")`, nil},
		{`name.matches("^a+$")`, filter.ErrUnsupportedOperation},
		{`name.matches("(?:.|a)(?:.|a)$")`, filter.ErrUnsupportedOperation},
		{`name.matches("\\d")`, filter.ErrUnsupportedOperation},
		{`name.matches("(?i)ü")`, filter.ErrUnsupportedOperation},
	} {
		t.Run("regex/"+tc.expr, func(t *testing.T) {
			t.Parallel()

			_, _, err := regex.Translate(filtertest.MustParseFilter(t, tc.expr))
			if tc.want == nil {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, tc.want)
		})
	}
}
