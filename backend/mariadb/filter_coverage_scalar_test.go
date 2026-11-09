// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb_test

import (
	"errors"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/metrics"
)

// countingMeter counts the translations a FilterTranslator times through its collector.
type countingMeter struct {
	subsystem string
	starts    atomic.Int64
	stops     atomic.Int64
}

func (m *countingMeter) Counter(metrics.MetricOpts) metrics.Counter {
	return metrics.Noop().Counter(metrics.MetricOpts{})
}
func (m *countingMeter) MustCounter(metrics.MetricOpts) metrics.Counter {
	return m.Counter(metrics.MetricOpts{})
}
func (m *countingMeter) Timer(metrics.HistogramOpts) metrics.Timer       { return countingTimer{m} }
func (m *countingMeter) MustTimer(o metrics.HistogramOpts) metrics.Timer { return m.Timer(o) }
func (m *countingMeter) WithSubsystem(s string) metrics.Meter            { m.subsystem = s; return m }

type countingTimer struct{ m *countingMeter }

func (t countingTimer) Start() func() {
	t.m.starts.Add(1)
	return func() { t.m.stops.Add(1) }
}

func (countingTimer) ObserveDuration(time.Duration) {}

func scalarPolicy(tb testing.TB, opts ...filter.PolicyOption) *filter.Policy {
	tb.Helper()

	return filtertest.MustPolicy(tb, append([]filter.PolicyOption{filter.WithAnyField(true)}, opts...)...)
}

func TestScalarOptions(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		opt  mariadb.FilterOption
		want error
	}{
		"decimal precision zero":  {mariadb.WithFilterDecimalFormat(sqlbase.DecimalFormat{Precision: 0}), filter.ErrInvalidOptions},
		"decimal scale too large": {mariadb.WithFilterDecimalFormat(sqlbase.DecimalFormat{Precision: 10, Scale: 11}), filter.ErrInvalidOptions},
		"nil collector":           {mariadb.WithFilterCollector(nil), nil},
		"typed nil collector":     {mariadb.WithFilterCollector((*countingMeter)(nil)), nil},
		"default decimal format":  {mariadb.WithFilterDecimalFormat(sqlbase.DefaultDecimalFormat()), nil},
		"narrow decimal format":   {mariadb.WithFilterDecimalFormat(sqlbase.DecimalFormat{Precision: 1, Scale: 1}), nil},
		"nil literal encoder":     {mariadb.WithFilterLiteralEncoder(nil), nil},
		"nil predicate renderer":  {mariadb.WithFilterPredicateRenderer(nil), nil},
		"empty filter options":    {mariadb.WithFilterOptions(), nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tr, err := mariadb.NewFilterTranslator(scalarPolicy(t), tc.opt)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				require.Nil(t, tr)

				return
			}

			require.NoError(t, err)

			sql, args, err := tr.Translate(filtertest.MustParseFilter(t, `a == 1`))
			require.NoError(t, err)
			assert.Equal(t, "`a` = ?", sql)
			assert.Equal(t, []any{int64(1)}, args)
		})
	}
}

func TestScalarConstructionErrors(t *testing.T) {
	t.Parallel()

	_, err := mariadb.NewFilterTranslator(nil)
	require.ErrorIs(t, err, filter.ErrPolicyRequired)

	// A field mapping onto an invalid column name surfaces at translation, not construction.
	tr, err := mariadb.NewFilterTranslator(scalarPolicy(t), mariadb.WithFilterOptions(filter.WithFieldMapping(map[string]string{"a": "t.a"})))
	require.NoError(t, err)

	sql, _, err := tr.Translate(filtertest.MustParseFilter(t, `a == 1`))
	require.NoError(t, err)
	assert.Equal(t, "`t`.`a` = ?", sql)
}

func TestScalarCollectorTimesEveryEntryPoint(t *testing.T) {
	t.Parallel()

	meter := &countingMeter{}
	tr, err := mariadb.NewFilterTranslator(scalarPolicy(t), mariadb.WithFilterCollector(meter))
	require.NoError(t, err)
	assert.Equal(t, "filter_mariadb", meter.subsystem)

	node := filtertest.MustParseFilter(t, `a == "x"`)

	_, _, err = tr.Translate(node)
	require.NoError(t, err)

	_, err = tr.TranslateInline(node)
	require.NoError(t, err)

	require.NoError(t, tr.ValidateAST(node))
	require.Error(t, tr.ValidateAST(&filter.UnaryOpNode{Op: filter.OpSize, Operand: &filter.IdentNode{Name: "a"}}))

	assert.Equal(t, int64(4), meter.starts.Load())
	assert.Equal(t, int64(4), meter.stops.Load())
}

type opaque struct{ v string }

func TestScalarLiteralEncoder(t *testing.T) {
	t.Parallel()

	errEncode := errors.New("encode failed")
	encoder := func(v any) (any, error) {
		switch x := v.(type) {
		case string:
			if x == "bad" {
				return nil, errEncode
			}

			return strings.ToUpper(x), nil
		case opaque:
			return x, nil
		case decimal.Decimal:
			return x.String() + "!", nil
		default:
			return v, nil
		}
	}

	tr, err := mariadb.NewFilterTranslator(scalarPolicy(t, filter.WithArrayFields("tags"),
		filter.WithFieldTypes(map[string]filter.FieldKind{"price": filter.FieldKindDecimal})), mariadb.WithFilterLiteralEncoder(encoder))
	require.NoError(t, err)

	t.Run("bound", func(t *testing.T) {
		t.Parallel()

		sql, args, err := tr.Translate(filtertest.MustParseFilter(t, `name == "abc" && price > 1.5`))
		require.NoError(t, err)
		assert.Equal(t, "(`name` = ?) AND (`price` > ?)", sql)
		assert.Equal(t, []any{"ABC", "1.5!"}, args)
	})

	t.Run("inline", func(t *testing.T) {
		t.Parallel()

		sql, err := tr.TranslateInline(filtertest.MustParseFilter(t, `name == "abc"`))
		require.NoError(t, err)
		assert.Equal(t, "`name` = 'ABC'", sql)
	})

	for _, expr := range []string{`name == "bad"`, `tags == "bad"`, `tags > "bad"`, `name.contains("bad")`} {
		t.Run("error/"+expr, func(t *testing.T) {
			t.Parallel()

			_, _, err := tr.Translate(filtertest.MustParseFilter(t, expr))
			require.ErrorIs(t, err, errEncode)
		})
	}

	t.Run("unsupported encoded type inline", func(t *testing.T) {
		t.Parallel()

		node := &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "name"}, Right: &filter.LiteralNode{Value: opaque{"x"}}}
		_, err := tr.TranslateInline(node)
		require.ErrorIs(t, err, filter.ErrUnsupportedType)
	})

	t.Run("undeclared array element of an opaque type", func(t *testing.T) {
		t.Parallel()

		node := &filter.BinaryOpNode{Op: filter.OpLT, Left: &filter.IdentNode{Name: "tags"}, Right: &filter.LiteralNode{Value: opaque{"x"}}}
		_, _, err := tr.Translate(node)
		require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
	})
}

func TestScalarPredicateRenderer(t *testing.T) {
	t.Parallel()

	errRender := errors.New("render failed")
	renderer := func(node filter.Node, value sqlbase.ValueFunc) (string, bool, error) {
		call, ok := node.(*filter.CallNode)
		if !ok || call.Op != filter.OpContains {
			return "", false, nil
		}

		lit, ok := call.Args[0].(*filter.LiteralNode)
		if !ok {
			return "", false, nil
		}

		if lit.Value == "boom" {
			return "", true, errRender
		}

		arg, err := value(lit.Value)
		if err != nil {
			return "", true, err
		}

		return "MATCH(`body`) AGAINST (" + arg + ")", true, nil
	}

	tr, err := mariadb.NewFilterTranslator(scalarPolicy(t), mariadb.WithFilterPredicateRenderer(renderer))
	require.NoError(t, err)

	sql, args, err := tr.Translate(filtertest.MustParseFilter(t, `body.contains("go") && n == 1`))
	require.NoError(t, err)
	assert.Equal(t, "(MATCH(`body`) AGAINST (?)) AND (`n` = ?)", sql)
	assert.Equal(t, []any{"go", int64(1)}, args)

	inline, err := tr.TranslateInline(filtertest.MustParseFilter(t, `body.contains("it's")`))
	require.NoError(t, err)
	assert.Equal(t, "MATCH(`body`) AGAINST ('it''s')", inline)

	sql, args, err = tr.Translate(filtertest.MustParseFilter(t, `body.startsWith("go")`))
	require.NoError(t, err)
	assert.Equal(t, "LOCATE(?, `body`) = 1", sql)
	assert.Equal(t, []any{"go"}, args)

	_, _, err = tr.Translate(filtertest.MustParseFilter(t, `body.contains("boom")`))
	require.ErrorIs(t, err, errRender)
}

func TestDecimalFormatEdges(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		format sqlbase.DecimalFormat
		valid  bool
	}{
		"default":            {sqlbase.DefaultDecimalFormat(), true},
		"smallest":           {sqlbase.DecimalFormat{Precision: 1}, true},
		"widest":             {sqlbase.DecimalFormat{Precision: 65, Scale: 38}, true},
		"scale equals prec":  {sqlbase.DecimalFormat{Precision: 5, Scale: 5}, true},
		"zero precision":     {sqlbase.DecimalFormat{}, false},
		"precision too big":  {sqlbase.DecimalFormat{Precision: 66}, false},
		"negative scale":     {sqlbase.DecimalFormat{Precision: 10, Scale: -1}, false},
		"scale too big":      {sqlbase.DecimalFormat{Precision: 65, Scale: 39}, false},
		"scale above prec":   {sqlbase.DecimalFormat{Precision: 4, Scale: 5}, false},
		"negative precision": {sqlbase.DecimalFormat{Precision: -1}, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sql, err := tc.format.SQL()
			checkErr := tc.format.Check(int64(0))

			if !tc.valid {
				require.ErrorIs(t, tc.format.Validate(), filter.ErrInvalidOptions)
				require.ErrorIs(t, err, filter.ErrInvalidOptions)
				require.ErrorIs(t, checkErr, filter.ErrInvalidOptions)
				assert.Empty(t, sql)

				return
			}

			require.NoError(t, tc.format.Validate())
			require.NoError(t, err)
			require.NoError(t, checkErr)
			assert.True(t, strings.HasPrefix(sql, "DECIMAL("))
		})
	}

	format := sqlbase.DecimalFormat{Precision: 5, Scale: 2}

	for name, tc := range map[string]struct {
		value any
		want  error
	}{
		"fits":              {"999.99", nil},
		"negative fits":     {"-999.99", nil},
		"integer":           {int64(12), nil},
		"decimal":           {decimal.RequireFromString("1.5"), nil},
		"trailing zeros":    {"1.500", nil},
		"rounds":            {"1.005", filter.ErrFieldTypeMismatch},
		"overflows":         {"1000", filter.ErrFieldTypeMismatch},
		"negative overflow": {"-1000.00", filter.ErrFieldTypeMismatch},
		"not a number":      {"abc", filter.ErrFieldTypeMismatch},
		"unsupported type":  {true, filter.ErrFieldTypeMismatch},
	} {
		t.Run("check/"+name, func(t *testing.T) {
			t.Parallel()

			err := format.Check(tc.value)
			if tc.want == nil {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestScalarDecimalOperands(t *testing.T) {
	t.Parallel()

	policy := scalarPolicy(t, filter.WithArrayFields("prices"),
		filter.WithFieldTypes(map[string]filter.FieldKind{"price": filter.FieldKindDecimal, "prices": filter.FieldKindDecimal}))
	tr, err := mariadb.NewFilterTranslator(policy, mariadb.WithFilterDecimalFormat(sqlbase.DecimalFormat{Precision: 6, Scale: 2}))
	require.NoError(t, err)

	for _, tc := range []struct {
		expr, sql, inline string
		args              []any
	}{
		{`price == 1.5`, "`price` = CAST(? AS DECIMAL(6,2))", "`price` = CAST('1.5' AS DECIMAL(6,2))", []any{"1.5"}},
		{`price >= "9999.99"`, "`price` >= CAST(? AS DECIMAL(6,2))", "`price` >= CAST('9999.99' AS DECIMAL(6,2))", []any{"9999.99"}},
		{`prices < 2`,
			"EXISTS (SELECT 1 FROM JSON_TABLE(`prices`, '$[*]' COLUMNS (v DECIMAL(6,2) PATH '$')) AS _aip_e WHERE _aip_e.v < CAST(? AS DECIMAL(6,2)))",
			"EXISTS (SELECT 1 FROM JSON_TABLE(`prices`, '$[*]' COLUMNS (v DECIMAL(6,2) PATH '$')) AS _aip_e WHERE _aip_e.v < CAST('2' AS DECIMAL(6,2)))",
			[]any{"2"}},
		{`prices == 2`, "JSON_CONTAINS(`prices`, JSON_ARRAY(CAST(? AS DECIMAL(6,2))))",
			"JSON_CONTAINS(`prices`, JSON_ARRAY(CAST('2' AS DECIMAL(6,2))))", []any{"2"}},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			node := filtertest.MustParseFilter(t, tc.expr)
			sql, args, err := tr.Translate(node)
			require.NoError(t, err)
			assert.Equal(t, tc.sql, sql)
			assert.Equal(t, tc.args, args)

			inline, err := tr.TranslateInline(node)
			require.NoError(t, err)
			assert.Equal(t, tc.inline, inline)
		})
	}

	for _, expr := range []string{`price == 10000`, `price == 0.001`, `prices > 10000`, `prices == "0.125"`} {
		t.Run("reject/"+expr, func(t *testing.T) {
			t.Parallel()

			_, _, err := tr.Translate(filtertest.MustParseFilter(t, expr))
			require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)
		})
	}
}

func TestScalarJSONArrayElements(t *testing.T) {
	t.Parallel()

	policy := scalarPolicy(t, filter.WithArrayFields("ints", "floats", "strs", "bools", "any", "times", "blobs"),
		filter.WithFieldTypes(map[string]filter.FieldKind{
			"ints": filter.FieldKindInt, "floats": filter.FieldKindFloat, "strs": filter.FieldKindString,
			"bools": filter.FieldKindBool, "times": filter.FieldKindTimestamp, "blobs": filter.FieldKindBytes,
		}))
	tr, err := mariadb.NewFilterTranslator(policy)
	require.NoError(t, err)

	table := func(col, typ, pred string) string {
		return "EXISTS (SELECT 1 FROM JSON_TABLE(`" + col + "`, '$[*]' COLUMNS (v " + typ + " PATH '$')) AS _aip_e WHERE " + pred + ")"
	}

	for _, tc := range []struct {
		expr, sql string
		args      []any
	}{
		{`ints > 1`, table("ints", "BIGINT", "_aip_e.v > ?"), []any{int64(1)}},
		{`floats <= 2.5`, table("floats", "DOUBLE", "_aip_e.v <= ?"), []any{2.5}},
		{`strs >= "a"`, table("strs", "LONGTEXT", "_aip_e.v >= ?"), []any{"a"}},
		{`any > 1`, table("any", "DOUBLE", "_aip_e.v > ?"), []any{int64(1)}},
		{`any > 1u`, table("any", "DOUBLE", "_aip_e.v > ?"), []any{uint64(1)}},
		{`any < 1.5`, table("any", "DOUBLE", "_aip_e.v < ?"), []any{1.5}},
		{`any < "m"`, table("any", "LONGTEXT", "_aip_e.v < ?"), []any{"m"}},
		{`1 < ints`, table("ints", "BIGINT", "_aip_e.v > ?"), []any{int64(1)}},
		{`ints == 1`, "JSON_CONTAINS(`ints`, JSON_ARRAY(?))", []any{int64(1)}},
		{`"a" in strs`, "JSON_CONTAINS(`strs`, JSON_ARRAY(?))", []any{"a"}},
		{`bools == true`, "JSON_CONTAINS(`bools`, 'true')", nil},
		{`bools == false`, "JSON_CONTAINS(`bools`, 'false')", nil},
		{`any == true`, "JSON_CONTAINS(`any`, 'true')", nil},
		{`size(strs) > 2`, "COALESCE(JSON_LENGTH(`strs`), 0) > ?", []any{int64(2)}},
		{`strs.contains("%")`, table("strs", "LONGTEXT", "LOCATE(?, _aip_e.v) > 0"), []any{"%"}},
		{`strs.startsWith("a")`, table("strs", "LONGTEXT", "LOCATE(?, _aip_e.v) = 1"), []any{"a"}},
		{`strs.matches("a$")`, table("strs", "LONGTEXT", `_aip_e.v REGEXP ?`), []any{`a\z`}},
		{`strs.endsWith("z")`, table("strs", "LONGTEXT",
			"(CHAR_LENGTH(_aip_e.v) >= CHAR_LENGTH(?) AND RIGHT(_aip_e.v, CHAR_LENGTH(?)) = ?)"), []any{"z", "z", "z"}},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			sql, args, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, err)
			assert.Equal(t, tc.sql, sql)
			assert.Equal(t, tc.args, args)
		})
	}

	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	for name, tc := range map[string]struct {
		node filter.Node
		want error
	}{
		"ordered boolean":           {filtertest.MustParseFilter(t, `bools > true`), filter.ErrUnsupportedOperation},
		"ordered undeclared bool":   {filtertest.MustParseFilter(t, `any <= false`), filter.ErrUnsupportedOperation},
		"timestamp element equal":   {binary(filter.OpEqual, "times", at), filter.ErrUnsupportedType},
		"timestamp element ordered": {binary(filter.OpLT, "any", at), filter.ErrUnsupportedType},
		"bytes element equal":       {binary(filter.OpEqual, "blobs", []byte("a")), filter.ErrUnsupportedType},
		"bytes element ordered":     {binary(filter.OpGT, "any", []byte("a")), filter.ErrUnsupportedType},
		"array against field":       {filtertest.MustParseFilter(t, `ints == other`), filter.ErrUnsupportedOperation},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, _, err := tr.Translate(tc.node)
			require.ErrorIs(t, err, tc.want)

			_, err = tr.TranslateInline(tc.node)
			require.ErrorIs(t, err, tc.want)
		})
	}

	t.Run("inline non-finite element", func(t *testing.T) {
		t.Parallel()

		for _, op := range []filter.Operator{filter.OpEqual, filter.OpLT} {
			_, err := tr.TranslateInline(binary(op, "floats", math.Inf(1)))
			require.ErrorIs(t, err, filter.ErrUnsupportedType)
		}
	})
}

func binary(op filter.Operator, field string, value any) *filter.BinaryOpNode {
	return &filter.BinaryOpNode{Op: op, Left: &filter.IdentNode{Name: field}, Right: &filter.LiteralNode{Value: value}}
}

func TestScalarInlineLiterals(t *testing.T) {
	t.Parallel()

	tr, err := mariadb.NewFilterTranslator(scalarPolicy(t))
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		value any
		want  string
	}{
		"true":       {true, "`f` = TRUE"},
		"false":      {false, "`f` = FALSE"},
		"int":        {int64(-7), "`f` = -7"},
		"uint":       {uint64(math.MaxUint64), "`f` = 18446744073709551615"},
		"float":      {1.25e-7, "`f` = 1.25e-07"},
		"bytes":      {[]byte{0x00, 0xff, 'a'}, "`f` = X'00ff61'"},
		"empty":      {"", "`f` = ''"},
		"timestamp":  {time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.FixedZone("x", 3600)), "`f` = '2026-01-02 02:04:05.123456'"},
		"escapes":    {"it's \\ \x00 \n \r \t ok", `` + "`f`" + ` = 'it''s \\ \0 \n \r \t ok'`},
		"multi-byte": {"привет 'мир'", "`f` = _utf8mb4 X'd0bfd180d0b8d0b2d0b5d1822027d0bcd0b8d18027'"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sql, err := tr.TranslateInline(binary(filter.OpEqual, "f", tc.value))
			require.NoError(t, err)
			assert.Equal(t, tc.want, sql)
		})
	}

	for name, v := range map[string]float64{"nan": math.NaN(), "+inf": math.Inf(1), "-inf": math.Inf(-1)} {
		t.Run("reject/"+name, func(t *testing.T) {
			t.Parallel()

			_, err := tr.TranslateInline(binary(filter.OpEqual, "f", v))
			require.ErrorIs(t, err, filter.ErrUnsupportedType)

			// The bound form passes the value to the driver untouched.
			_, args, err := tr.Translate(binary(filter.OpEqual, "f", v))
			require.NoError(t, err)
			require.Len(t, args, 1)
		})
	}

	_, _, err = tr.Translate(binary(filter.OpEqual, "f", opaque{"x"}))
	require.ErrorIs(t, err, filter.ErrUnsupportedType)

	tr, err = mariadb.NewFilterTranslator(scalarPolicy(t, filter.WithFieldTypes(map[string]filter.FieldKind{"a.b": filter.FieldKindString})))
	require.NoError(t, err)

	sql, err := tr.TranslateInline(filtertest.MustParseFilter(t, `a.b == "x" && c != d && size(s) > 1`))
	require.NoError(t, err)
	assert.Equal(t, "((`a`.`b` = 'x') AND (NOT (`c` <=> `d`))) AND (CHAR_LENGTH(`s`) > 1)", sql)
}

func TestScalarFieldComparisonsAndSubstring(t *testing.T) {
	t.Parallel()

	tr, err := mariadb.NewFilterTranslator(scalarPolicy(t))
	require.NoError(t, err)

	for _, tc := range []struct{ expr, sql string }{
		{`a == b`, "`a` <=> `b`"},
		{`a != b`, "NOT (`a` <=> `b`)"},
		{`size(name) == 3`, "CHAR_LENGTH(`name`) = ?"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			sql, _, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, err)
			assert.Equal(t, tc.sql, sql)
		})
	}

	for _, tc := range []struct {
		node filter.Node
		sql  string
	}{
		{&filter.CallNode{Op: filter.OpSubstring, Target: &filter.IdentNode{Name: "s"},
			Args: []filter.Node{&filter.LiteralNode{Value: int64(1)}}}, "SUBSTRING(`s`, 2)"},
		{&filter.CallNode{Op: filter.OpSubstring, Target: &filter.IdentNode{Name: "s"},
			Args: []filter.Node{&filter.LiteralNode{Value: int64(0)}, &filter.LiteralNode{Value: int64(3)}}}, "SUBSTRING(`s`, 1, 3)"},
	} {
		sql, err := tr.TranslateInline(&filter.BinaryOpNode{Op: filter.OpEqual, Left: tc.node, Right: &filter.LiteralNode{Value: "ab"}})
		require.NoError(t, err)
		assert.Contains(t, sql, tc.sql)
	}
}

func TestScalarRegexDialect(t *testing.T) {
	t.Parallel()

	re2, err := mariadb.NewFilterTranslator(scalarPolicy(t, filter.WithArrayFields("tags")))
	require.NoError(t, err)

	for _, tc := range []struct{ expr, arg string }{
		{`s.matches("a$")`, `a\z`},
		{`s.matches("(?m)a$")`, `(?m)a$`},
		{`s.matches("[$]")`, `[$]`},
		{`s.matches("a")`, `a`},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			sql, args, err := re2.Translate(filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, err)
			assert.Equal(t, "`s` REGEXP ?", sql)
			assert.Equal(t, []any{tc.arg}, args)
		})
	}

	custom, err := mariadb.NewFilterTranslator(scalarPolicy(t, filter.WithRegexValidator(func(string) error { return nil })))
	if err != nil {
		t.Skip("custom regex validator unavailable:", err)
	}

	sql, args, err := custom.Translate(filtertest.MustParseFilter(t, `s.matches("a$")`))
	require.NoError(t, err)
	assert.Equal(t, "`s` REGEXP ?", sql)
	require.Len(t, args, 1)
}
