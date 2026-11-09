// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory_test

import (
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/metrics"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

// sortedIDs sorts records with compare and returns their ids.
func sortedIDs(records []map[string]any, compare memory.Comparator) string {
	if compare != nil {
		slices.SortStableFunc(records, compare)
	}

	ids := make([]string, len(records))
	for i, r := range records {
		ids[i], _ = r["id"].(string)
	}

	return strings.Join(ids, " ")
}

func TestOrderTranslatorConformance(t *testing.T) {
	t.Parallel()

	orderbytest.RunConformance(t, orderbytest.Backend{
		New: func(policy *orderby.Policy, opts ...orderby.TranslatorOption) (orderbytest.TranslateFunc, error) {
			tr, err := memory.NewOrderTranslator(policy, memory.WithOrderOptions(opts...), memory.WithOrderCollector(metrics.Noop()))
			if err != nil {
				return nil, err
			}

			return func(spec orderby.Spec) (string, error) {
				compare, err := tr.Translate(spec)
				if err != nil || compare == nil {
					return "", err
				}

				return sortedIDs(orderbytest.ConformanceRecords(), compare), nil
			}, nil
		},
		// Records: 1 carol 30 Oslo, 2 alice null Bern, 3 bob 25.5 Lima, 4 dave (no age) Accra, 5 erin 30 Kyiv. Nulls sort first
		// ascending and last descending by default; a stable sort keeps 2 before 4 when they tie.
		Expect: map[string]orderbytest.Outcome{
			orderbytest.CaseEmpty:             {},
			orderbytest.CaseAscending:         {Want: "2 3 1 4 5"},
			orderbytest.CaseDescending:        {Want: "5 4 1 3 2"},
			orderbytest.CaseMultipleKeys:      {Want: "1 5 3 2 4"},
			orderbytest.CaseNestedPath:        {Want: "4 2 5 3 1"},
			orderbytest.CaseFieldMapping:      {Want: "3 5 1 4 2"},
			orderbytest.CasePrefixMapping:     {Want: "4 2 5 3 1"},
			orderbytest.CaseDefaultOrder:      {Want: "1 5 3 2 4"},
			orderbytest.CaseTiebreaker:        {Want: "2 3 1 4 5"},
			orderbytest.CaseTiebreakerPresent: {Want: "5 4 3 2 1"},
			orderbytest.CaseAscNullsFirst:     {Want: "2 4 3 1 5"},
			orderbytest.CaseAscNullsLast:      {Want: "3 1 5 2 4"},
			orderbytest.CaseDescNullsFirst:    {Want: "2 4 1 5 3"},
			orderbytest.CaseDescNullsLast:     {Want: "1 5 3 2 4"},
			orderbytest.CaseCollation:         {Err: orderby.ErrUnsupportedOrdering},
		},
	})
}

func mustComparator(t *testing.T, spec string, opts ...memory.OrderOption) memory.Comparator {
	t.Helper()

	tr, err := memory.NewOrderTranslator(orderbytest.MustPolicy(t), opts...)
	require.NoError(t, err)

	compare, err := tr.Translate(orderbytest.MustParseOrderBy(t, spec))
	require.NoError(t, err)
	require.NotNil(t, compare)

	return compare
}

type (
	namedInt    int16
	namedString string
)

func TestComparator_Values(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	n := int64(7)

	for _, tc := range []struct {
		name string
		a, b any
		want int
	}{
		{name: "int vs float exact", a: int64(1 << 53), b: float64(1<<53) + 2, want: -1},
		{name: "int vs float equal", a: 3, b: 3.0, want: 0},
		{name: "decimal vs int", a: decimal.RequireFromString("2.5"), b: int64(2), want: 1},
		{name: "uint above int64", a: uint64(math.MaxUint64), b: int64(math.MaxInt64), want: 1},
		{name: "named int", a: namedInt(-3), b: int32(1), want: -1},
		{name: "named string", a: namedString("b"), b: "a", want: 1},
		{name: "strings bytewise", a: "B", b: "a", want: -1},
		{name: "number before string", a: int64(99), b: "1", want: -1},
		{name: "string before bool", a: "z", b: false, want: -1},
		{name: "bool order", a: false, b: true, want: -1},
		{name: "bool before time", a: true, b: ts, want: -1},
		{name: "time sub-millisecond", a: ts, b: ts.Add(time.Nanosecond), want: -1},
		{name: "time zones", a: ts, b: ts.In(time.FixedZone("x", 3600)), want: 0},
		{name: "pointer", a: &n, b: int64(8), want: -1},
		{name: "nil pointer is null", a: (*int64)(nil), b: int64(-100), want: -1},
		{name: "NaN before numbers", a: math.NaN(), b: math.Inf(-1), want: -1},
		{name: "scalar before document", a: ts, b: map[string]any{"x": 1}, want: -1},
		{name: "scalar before array", a: ts, b: []any{1}, want: -1},
		{name: "scalar before bytes", a: ts, b: []byte("x"), want: -1},
		{name: "unsortable equal", a: []any{1}, b: map[string]any{}, want: 0},
		{name: "nil slice is null", a: []any(nil), b: int64(0), want: -1},
		{name: "null equals missing", a: nil, b: nil, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			compare := mustComparator(t, "v")
			got := compare(map[string]any{"v": tc.a}, map[string]any{"v": tc.b})
			require.Equal(t, tc.want, sign(got))
			require.Equal(t, -tc.want, sign(compare(map[string]any{"v": tc.b}, map[string]any{"v": tc.a})))
		})
	}
}

func sign(c int) int {
	switch {
	case c < 0:
		return -1
	case c > 0:
		return 1
	default:
		return 0
	}
}

func TestComparator_Paths(t *testing.T) {
	t.Parallel()

	compare := mustComparator(t, "a.b.c desc")

	typed := map[string]map[string]int{"b": {"c": 5}}
	records := []map[string]any{
		{"id": "missing"},
		{"id": "scalar parent", "a": 1},
		{"id": "typed maps", "a": typed},
		{"id": "pointer", "a": &map[string]any{"b": map[string]any{"c": 9}}},
		{"id": "nil map", "a": map[string]any(nil)},
		{"id": "array parent", "a": []any{map[string]any{"b": map[string]any{"c": 100}}}},
	}

	require.Equal(t, "pointer typed maps missing scalar parent nil map array parent", sortedIDs(records, compare))
}

func TestComparator_ConcurrentUse(t *testing.T) {
	t.Parallel()

	compare := mustComparator(t, "age desc, name")

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			records := orderbytest.ConformanceRecords()
			slices.SortStableFunc(records, compare)
			assert.Equal(t, "1", records[0]["id"])
		})
	}

	wg.Wait()
}

// encoding stores a wrapper struct as its inner value; it charges one unit per read.
type encoding struct{}

type wrapped struct{ v int64 }

type selfEncoded struct{ v string }

var selfType = reflect.TypeFor[*selfEncoded]()

func (encoding) Stored(v any, b evaluator.Budget) (any, bool) {
	if w, ok := v.(wrapped); ok {
		if !b.Charge(1) {
			return v, true
		}

		return w.v, true
	}

	return nil, false
}

func (encoding) Encode(v any, b evaluator.Budget) any {
	if s, ok := v.(*selfEncoded); ok && b.Charge(1) {
		return s.v
	}

	return v
}

func (encoding) EncodesItself(t reflect.Type) bool { return t == selfType }
func (encoding) Owns(reflect.Type) bool            { return false }

func TestComparator_ValueEncoding(t *testing.T) {
	t.Parallel()

	records := func() []map[string]any {
		return []map[string]any{
			{"id": "w9", "v": wrapped{9}},
			{"id": "w1", "v": wrapped{1}},
			{"id": "i5", "v": int64(5)},
		}
	}

	require.Equal(t, "w1 i5 w9", sortedIDs(records(), mustComparator(t, "v", memory.WithOrderValueEncoding(encoding{}))))
	// Without the encoding a struct is unsortable and sorts after every scalar.
	require.Equal(t, "i5 w9 w1", sortedIDs(records(), mustComparator(t, "v")))
	// A budget of one unit is enough for one read; a non-positive budget is ignored.
	require.Equal(t, "w1 i5 w9", sortedIDs(records(), mustComparator(t, "v",
		memory.WithOrderValueEncoding(encoding{}), memory.WithOrderMaxDataBudget(1), memory.WithOrderMaxDataBudget(0))))

	self := []map[string]any{{"id": "b", "v": &selfEncoded{"b"}}, {"id": "a", "v": &selfEncoded{"a"}}}
	require.Equal(t, "a b", sortedIDs(self, mustComparator(t, "v", memory.WithOrderValueEncoding(encoding{}))))
}

func TestComparator_BudgetExhausted(t *testing.T) {
	t.Parallel()

	tr, err := memory.NewOrderTranslator(orderbytest.MustPolicy(t), memory.WithOrderValueEncoding(exhausting{}))
	require.NoError(t, err)

	compare, err := tr.Translate(orderbytest.MustParseOrderBy(t, "v"))
	require.NoError(t, err)

	records := []map[string]any{{"id": "w", "v": wrapped{1}}, {"id": "i", "v": int64(5)}}
	require.Equal(t, "i w", sortedIDs(records, compare))
}

// exhausting spends more than any budget on every read.
type exhausting struct{ encoding }

func (exhausting) Stored(v any, b evaluator.Budget) (any, bool) {
	if _, ok := v.(wrapped); ok {
		b.Charge(math.MaxInt)
		return v, true
	}

	return nil, false
}

func TestOrderTranslator_Errors(t *testing.T) {
	t.Parallel()

	_, err := memory.NewOrderTranslator(nil)
	require.ErrorIs(t, err, orderby.ErrPolicyRequired)

	tr, err := memory.NewOrderTranslator(orderbytest.MustPolicy(t), memory.WithOrderCollector(metrics.Noop()))
	require.NoError(t, err)

	compare, err := tr.Translate(orderby.Spec{})
	require.NoError(t, err)
	require.Nil(t, compare)

	_, err = tr.Translate(orderby.Spec{Keys: []orderby.Key{{Name: "a", Collation: "C"}}})
	require.ErrorIs(t, err, orderby.ErrUnsupportedOrdering)
}
