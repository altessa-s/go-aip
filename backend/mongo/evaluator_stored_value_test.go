// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// Named types the evaluator reads by their base kind.
type (
	label   string
	counter int16
)

// chainNode is a struct the driver encodes field by field; Next may close a cycle, Parent may too, but the driver skips it.
type chainNode struct {
	Name   string     `bson:"name"`
	Next   *chainNode `bson:"next"`
	Parent *chainNode `bson:"-"`
	Items  []any      `bson:"items"`
}

// shared is a node whose two fields point to the same child: its encoding doubles at every level.
type shared struct {
	A, B *shared
}

// chain returns a list of n nodes named "0", "1", ...
func chain(n int) *chainNode {
	head := &chainNode{Name: "0"}
	for cur, i := head, 1; i < n; i++ {
		cur.Next = &chainNode{Name: strconv.Itoa(i), Parent: cur}
		cur = cur.Next
	}

	return head
}

// TestEvaluator_CyclicValuesOpaque: the cases of review finding R11-C1 — a cyclic value used to overflow the stack, a fatal error
// that kills the process (verified on the unfixed code: "fatal error: stack overflow"). A value the driver cannot store, cyclic or
// beyond MongoDB's nesting limit, is opaque: it matches nothing but !=, in bounded time.
func TestEvaluator_CyclicValuesOpaque(t *testing.T) {
	t.Parallel()

	selfNode := &chainNode{Name: "a"}
	selfNode.Next = selfNode

	var selfIface any

	selfIface = &selfIface
	selfMap := map[string]any{}
	selfMap["a"] = selfMap
	intMap := map[int]any{}
	intMap[1] = intMap
	selfList := []any{nil}
	selfList[0] = selfList
	listNode := &chainNode{Name: "a"}
	listNode.Items = []any{listNode}

	var dag *shared
	for range 64 {
		dag = &shared{A: dag, B: dag}
	}

	pointerLoop := []any{nil}
	pointerLoop[0] = &pointerLoop[0]

	values := map[string]any{
		"self-referencing struct":            selfNode,
		"self-referencing *any":              selfIface,
		"pointer to itself in a list":        pointerLoop,
		"struct holding a cyclic map":        struct{ M map[string]any }{selfMap},
		"cyclic map with integer keys":       intMap,
		"struct holding a cyclic list":       struct{ L []any }{selfList},
		"struct cycle through a list":        listNode,
		"exponential shared pointers":        dag,
		"chain deeper than MongoDB's limit":  chain(150),
		"non-string keys deeper than limits": map[int]any{1: chain(150)},
	}
	for name, v := range values {
		data := map[string]any{"v": v}

		opts := []any{withPolicy(filter.WithArrayFields("v.name")), filtereval.WithProfile(mongo.DefaultSchema())}

		eval, err := newEvaluator(opts...)
		require.NoError(t, err)

		for _, expr := range []string{`v.name == "a"`, `v.name == "0"`, `v.name.startsWith("a")`, "has(v.name)", "v == null"} {
			got, err := eval.Evaluate(filtertest.MustParseFilter(t, expr), data)
			require.NoError(t, err, "%s: %s", name, expr)
			require.False(t, got, "%s: %s", name, expr)
		}

		got, err := mustMongoEvaluator(t).Evaluate(filtertest.MustParseFilter(t, "v != 1"), data)
		require.NoError(t, err)
		require.True(t, got, name)
	}
}
