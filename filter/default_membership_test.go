// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// Comparable Go types the default evaluator does not know, holding interfaces.
type (
	memberBox    struct{ V any }
	memberPair   [2]any
	memberNested struct {
		B memberBox
		P *int64
		S string
	}
	// scopedCode has the shape of the MongoDB driver's CodeWithScope: a string and an interface.
	scopedCode struct {
		Code  string
		Scope any
	}
)

// TestEvaluator_DefaultMembershipByDynamicValue: review finding R21-2 — the default evaluator's `in` compared two data values of a
// type holding an interface as unequal, whatever they held, where it had compared them as Go's == does after
// reflect.Value.Comparable (verified on 570068de: memberBox{1} in [memberBox{1}] is false). An interface inside is compared by the
// dynamic value it holds: equal values of comparable dynamic types are members, and a value holding an uncomparable one (a slice,
// a map) is a member of nothing, as before.
func TestEvaluator_DefaultMembershipByDynamicValue(t *testing.T) {
	t.Parallel()

	x, y := int64(7), int64(7)
	long := strings.Repeat("s", 1000)
	nested := memberNested{B: memberBox{V: int64(1)}, P: &x, S: long}
	scoped := scopedCode{Code: "f()", Scope: int64(1)}
	documentScoped := scopedCode{Code: "f()", Scope: []any{}}
	cases := []struct {
		name string
		v    any
		w    []any
		want bool
	}{
		{"equal dynamic values", memberBox{V: int64(1)}, []any{memberBox{V: int64(2)}, memberBox{V: int64(1)}}, true},
		{"unequal dynamic values", memberBox{V: int64(1)}, []any{memberBox{V: int64(2)}}, false},
		{"dynamic values of two types", memberBox{V: int64(1)}, []any{memberBox{V: int32(1)}}, false},
		{"nil interfaces", memberBox{}, []any{memberBox{}}, true},
		{"nil against a value", memberBox{}, []any{memberBox{V: int64(1)}}, false},
		{"uncomparable dynamic value", memberBox{V: []int{1}}, []any{memberBox{V: []int{1}}}, false},
		{"uncomparable dynamic value in the list", memberBox{V: int64(1)}, []any{memberBox{V: map[string]int{}}, memberBox{V: int64(1)}}, true},
		{"uncomparable against comparable", memberBox{V: map[string]int{}}, []any{memberBox{V: int64(1)}}, false},
		{"nested interfaces", memberBox{V: memberBox{V: "a"}}, []any{memberBox{V: memberBox{V: "a"}}}, true},
		{"nested uncomparable", memberBox{V: memberBox{V: []string{"a"}}}, []any{memberBox{V: memberBox{V: []string{"a"}}}}, false},
		{"array of interfaces", memberPair{int64(1), "a"}, []any{memberPair{int64(1), "a"}}, true},
		{"array of interfaces differing", memberPair{int64(1), "a"}, []any{memberPair{int64(1), "b"}}, false},
		{"array holding a slice", memberPair{int64(1), []int{}}, []any{memberPair{int64(1), []int{}}}, false},
		{"same pointer inside", nested, []any{nested}, true},
		{"equal targets of two pointers", memberNested{B: memberBox{V: int64(1)}, P: &x}, []any{memberNested{B: memberBox{V: int64(1)}, P: &y}}, false},
		{"pointer in an interface", memberBox{V: &x}, []any{memberBox{V: &x}}, true},
		{"long strings in interfaces", memberBox{V: long}, []any{memberBox{V: long + ""}}, true},
		{"CodeWithScope without a scope", scopedCode{Code: "f()"}, []any{scopedCode{Code: "f()"}}, true},
		{"CodeWithScope with a comparable scope", scoped, []any{scoped}, true},
		{"CodeWithScope with a document scope", documentScoped, []any{documentScoped}, false},
		{"value of another type", memberBox{V: int64(1)}, []any{int64(1)}, false},
	}
	eval, err := newEvaluator()
	require.NoError(t, err)

	node := filtertest.MustParseFilter(t, "v in w")
	for _, tc := range cases {
		got, err := eval.Evaluate(node, map[string]any{"v": tc.v, "w": tc.w})
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, got, tc.name)
	}
}

// TestEvaluator_WideStructMembershipCharged: review finding R22-2 — the default evaluator's `in` between two values of a comparable
// struct type holding a string pushed every field of the struct before charging their visits, so a struct of a string and 65,536
// more fields was enumerated, and its fields allocated, whatever the data budget (verified on c87267e6: megabytes allocated under a
// budget of 100). The fields are charged before they are pushed, and type inspection is charged as it goes: the comparison fails
// with ErrDataLimitExceeded before the walk allocates.
func TestEvaluator_WideStructMembershipCharged(t *testing.T) { //nolint:paralleltest // measures allocations
	fields := make([]reflect.StructField, 1+1<<16)

	fields[0] = reflect.StructField{Name: "S", Type: reflect.TypeFor[string]()}
	for i := 1; i < len(fields); i++ {
		fields[i] = reflect.StructField{Name: "F" + strconv.Itoa(i), Type: reflect.TypeFor[int64]()}
	}

	v := reflect.New(reflect.StructOf(fields)).Elem().Interface()
	data := map[string]any{"v": v}
	node := filtertest.MustParseFilter(t, "v in [v]")

	limited, err := newEvaluator(filtereval.WithMaxDataBudget(100))
	require.NoError(t, err)

	elapsed, allocated := measured(func() { _, err = limited.Evaluate(node, data) })

	require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
	requireQuick(t, elapsed, quickEvaluation)
	require.Less(t, allocated, uint64(64<<10), "the fields are not pushed before they are charged")

	plain, err := newEvaluator()
	require.NoError(t, err)
	got, err := plain.Evaluate(node, data)
	require.NoError(t, err)
	require.True(t, got, "within the default budget the value is a member of a list holding it")
}
