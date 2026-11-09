// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"os"
	"reflect"
	"runtime/metrics"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-atlas-core/types/optional"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

// FuzzEvaluateDataShapes evaluates filters against data values built from the fuzz input — typed and named slices, arrays and
// maps, pointers, interfaces, cycles, shared DAGs, structs, decimals, long keys and strings — and checks that no evaluation does work proportional to the size of its data before charging it: each one
// finishes within a time and an allocation bound (or fails with ErrDataLimitExceeded within them), in both modes, under a small
// data budget and the default one. The seed corpus, which `go test` runs, holds every shape at its largest. Under -fuzz a
// watchdog also stops a worker whose evaluation hangs or whose heap grows beyond any bound, so that the input is reported.
func FuzzEvaluateDataShapes(f *testing.F) {
	for shape := range shapeCount {
		f.Add([]byte{byte(shape), 0xff, 0xff, 0xff, 0xff, byte(shape), 0xff, 0xff}, uint8(shape))
		f.Add([]byte{byte(shape), 3, 1, 2, 7, 0, 5}, uint8(shape*7+1))
	}
	// The shapes of review round 13: hooks returning 8 MiB, an opaque array in a list of itself, a long key repeated in a
	// document, a decimal coefficient of 200,001 digits nested in a struct. The evaluators read no value encoding (the MongoDB
	// driver's is covered by the value encoding tests), so the driver's types are not generated and the hooks and structs are opaque values.
	f.Add([]byte{shapeHooks, 0xff, 1}, uint8(exprIndex("v == 1")))
	f.Add([]byte{shapeZeroArray, 30}, uint8(exprIndex("v in [v]")))
	f.Add([]byte{shapeZeroArray, 30, shapeList, 1, shapeZeroArray, 30}, uint8(exprIndex("v in w")))
	f.Add([]byte{shapeRepeatedKeys, 0xff}, uint8(exprIndex("v.x == 1")))
	f.Add([]byte{shapeDecimal, 2, 1}, uint8(exprIndex("has(v.d)")))
	// The shapes of review round 14: hooks returning 8 MiB only when called again, an empty array field in a long data list, struct
	// types constructed at run time with 32,768 fields or with long tags, hooks through the pointer to the struct holding them.
	f.Add([]byte{shapeHooks, 2, 0, 6}, uint8(exprIndex("v == 1")))
	f.Add([]byte{shapeLongList, 0, shapeLongList, 1}, uint8(exprIndex("v in w")))
	f.Add([]byte{shapeLongList, 0, shapeLongList, 2}, uint8(exprIndex("v in w")))
	f.Add([]byte{shapeWideStruct, 0}, uint8(exprIndex("v == 1")))
	f.Add([]byte{shapeWideStruct, 1}, uint8(exprIndex("has(v.a)")))
	f.Add([]byte{shapeWideStruct, 0, shapeDecimal, 0, 0, 0}, uint8(exprIndex("v == w")))
	f.Add([]byte{shapeHooks, 2, 0, 7}, uint8(exprIndex("has(v.a)")))

	evals := fuzzEvaluators(f)
	exprs := fuzzFilters()

	fuzzShared() // outside the measured evaluations: a hook builds it otherwise

	if fuzzing() {
		startWatchdog()
	}

	f.Fuzz(func(t *testing.T, input []byte, expr uint8) {
		gen := shapeReader{in: input}
		data := map[string]any{"v": gen.value(0), "w": gen.value(0)}
		node := exprs[int(expr)%len(exprs)]

		for _, e := range evals {
			var err error

			evaluating.Store(time.Now().UnixNano())

			elapsed, allocated := measured(func() { _, err = e.eval.Evaluate(node.node, data) })

			evaluating.Store(0)
			requireQuick(t, elapsed, e.maxTime, "%s: %s (%v)", e.name, node.text, err)
			require.Less(t, allocated, e.maxAlloc, "%s: %s (%v)", e.name, node.text, err)
		}
	})
}

// fuzzEvaluator is an evaluator of the fuzz test with the bounds of its evaluations.
type fuzzEvaluator struct {
	name     string
	eval     *filtereval.Evaluator
	maxTime  time.Duration
	maxAlloc uint64
}

// smallDataBudget is the data budget of the evaluators whose allocations the fuzz test bounds tightly: everything an evaluation
// allocates is charged to it, at a few hundred bytes a unit at most, or bounded by a fixed cap.
const smallDataBudget = 1 << 16

// fuzzEvaluators returns the evaluators of the fuzz test: the MongoDB profile, with and without array fields, under a small data budget
// with tight bounds and under the default one with bounds that only catch work beyond any budget.
func fuzzEvaluators(tb testing.TB) []fuzzEvaluator {
	tb.Helper()

	arrays := filter.WithArrayFields("v", "w", "v.a")

	var out []fuzzEvaluator

	for _, budget := range []struct {
		name     string
		opts     []any
		maxTime  time.Duration
		maxAlloc uint64
	}{
		{"small budget", []any{filtereval.WithMaxDataBudget(smallDataBudget)}, 2 * time.Second, 64 << 20},
		{"default budget", nil, 10 * time.Second, 1 << 30},
	} {
		for _, mode := range []struct {
			name string
			opts []any
		}{
			{"mongo", []any{filtereval.WithProfile(mongo.DefaultSchema())}},
			{"mongo arrays", []any{filtereval.WithProfile(mongo.DefaultSchema()), withPolicy(arrays)}},
		} {
			eval, err := newEvaluator(append(append([]any{}, budget.opts...), mode.opts...)...)
			require.NoError(tb, err)

			out = append(out, fuzzEvaluator{
				name: budget.name + ", " + mode.name, eval: eval, maxTime: budget.maxTime, maxAlloc: budget.maxAlloc,
			})
		}
	}

	return out
}

// fuzzExpressions are the filters the fuzz test evaluates: comparisons, membership in literal and data lists, paths, has(),
// size(), the string functions and substring, on the fields v and w.
var fuzzExpressions = []string{
	"v == 1", "v != 1", "v > 0", `v < "m"`, `v == "x"`, "v == null", "v == true", "v == 1.5", "v >= -1e308",
	`v in [1, "x", true, null]`, "v in [v]", "v in w", "w in [v, w]", "v == w", "v == v",
	"v.a == 1", "v.a.a.a.a.a == 1", `v.x.y == "x"`, "v.x == 1", "has(w.a)", "has(v.a)", "has(v.a.b.c)", "has(v.d)", "v.d == 1",
	"v.d > 0", "size(v) == 1", "size(v) > 1", "size(v.a) == 2", `v.contains("x")`, `v.startsWith("x")`, `v.endsWith("k")`,
	`v.matches("^x+$")`, `v.a.contains("x")`, `v.substring(0, 1) == "x"`, `v.s == "x"`, "v.a in [1, 2]",
	"!(v == 1) && w != 2", "v == 1 || w == 1 || v.a.a == 2",
}

// parsedFilter is a fuzz expression and its tree.
type parsedFilter struct {
	text string
	node filter.Node
}

// fuzzFilters parses [fuzzExpressions] once.
var fuzzFilters = sync.OnceValue(func() []parsedFilter {
	parser, err := filterparser.New()
	if err != nil {
		panic(err)
	}

	out := make([]parsedFilter, len(fuzzExpressions))
	for i, text := range fuzzExpressions {
		node := parser.MustParse(text)
		out[i] = parsedFilter{text: text, node: node}
	}

	return out
})

// exprIndex returns the index of text in [fuzzExpressions].
func exprIndex(text string) int {
	for i, e := range fuzzExpressions {
		if e == text {
			return i
		}
	}

	panic("unknown fuzz expression " + text)
}

// fuzzing reports whether the test binary fuzzes (-fuzz), as a fuzz worker does, rather than running the seed corpus.
func fuzzing() bool {
	return slices.ContainsFunc(os.Args[1:], func(arg string) bool {
		return strings.HasPrefix(arg, "-test.fuzz=") || strings.HasPrefix(arg, "-test.fuzzworker")
	})
}

// Bounds the watchdog enforces while fuzzing, beyond which a worker panics: an evaluation would OOM the machine or hang before
// the test could measure it.
const (
	watchdogHeap = 2 << 30
	watchdogHang = 30 * time.Second
)

// evaluating holds the start of the evaluation in progress, in Unix nanoseconds, or 0.
var evaluating atomic.Int64

// startWatchdog starts, once, the goroutine that panics when the heap exceeds [watchdogHeap] or an evaluation runs longer than
// [watchdogHang]; the panic stops the fuzz worker, which reports its input.
var startWatchdog = sync.OnceFunc(func() {
	go func() {
		sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
		for range time.Tick(10 * time.Millisecond) {
			metrics.Read(sample)

			if heap := sample[0].Value.Uint64(); heap > watchdogHeap {
				panic(fmt.Sprintf("fuzz watchdog: heap of %d bytes", heap))
			}

			if start := evaluating.Load(); start != 0 && time.Since(time.Unix(0, start)) > watchdogHang {
				panic("fuzz watchdog: an evaluation runs for " + watchdogHang.String())
			}
		}
	}()
})

// The shapes a fuzz input selects by its first byte, and then by the bytes each shape reads.
const (
	shapeLeaf = iota
	shapeList
	shapeMap
	shapeDocument
	shapeRepeatedKeys
	shapeBSONArray
	shapeRaw
	shapeTypedSlice
	shapeZeroArray
	shapeArray
	shapePointer
	shapeCycle
	shapeDAG
	shapeHooks
	shapeDecimal
	shapeDecimal128
	shapeTypedMap
	shapeStruct
	shapeLongString
	shapeBSONTypes
	shapeLongList
	shapeWideStruct
	shapeCount
)

// Bounds of what a fuzz input builds, so that a worker holds a few megabytes per value: the shared values below make the large
// shapes without allocating them.
const (
	maxShapeDepth  = 4
	maxChildren    = 8
	maxSliceLength = 1 << 18
)

// fuzzShared holds the large values the shapes reuse: an 8 MiB string, a 64 KiB key, powers of ten of up to 200,000 digits, lists
// of a million nulls and integers, and struct types constructed at run time of 32,768 fields and of 1000 fields with tags of 16 KiB.
var fuzzShared = sync.OnceValue(func() (s struct {
	long, key    string
	powers       []*big.Int
	nulls, ints  []any
	wide, tagged reflect.Type
}) {
	s.long = strings.Repeat("x", 8<<20)

	s.key = strings.Repeat("k", 64<<10)
	for _, digits := range []int64{3, 40, 5000, 200_000} {
		s.powers = append(s.powers, new(big.Int).Exp(big.NewInt(10), big.NewInt(digits), nil))
	}

	s.nulls, s.ints = make([]any, 1<<20), make([]any, 1<<20)
	for i := range s.ints {
		s.ints[i] = int64(i)
	}

	s.wide, s.tagged = wideStruct(1<<15, 0), wideStruct(1000, 8191)

	return s
})

// fuzzHook stands for a value encoding itself as a string of 16 bytes or 8 MiB; read without an encoding, it is an opaque struct.
type fuzzHook struct{ Large bool }

// fuzzGrowingHook stands for a value whose encoding grows when it is asked again; it is an opaque struct.
type fuzzGrowingHook struct{ calls *int }

// fuzzDocumentHook stands for a value encoding itself as a document of 8 MiB; it is an opaque struct.
type fuzzDocumentHook struct{}

// fuzzPointerHook stands for a value encoding itself through its pointer; it is an opaque struct.
type fuzzPointerHook struct{}

// fuzzNode is a linked node, which a cycle makes infinitely deep.
type fuzzNode struct {
	Name string
	Next *fuzzNode
}

// fuzzInline inlines itself, which the driver cannot describe.
type fuzzInline struct {
	Next *fuzzInline `bson:",inline"`
}

// fuzzWide has a field whose key is long.
type fuzzWide struct {
	AFieldWhoseKeyIsMuchLongerThanItsValueSoThatTheEncodingIsMostlyKeysAndNotValuesAtAll int8
}

// fuzzHolder holds values of every kind a struct field can hold.
type fuzzHolder struct {
	A any
	D decimal.Decimal
	S string
	L []any
	M map[string]any
	P *fuzzHolder
}

// Named types the driver encodes by their kind.
type (
	fuzzInts    []int64
	fuzzStrings []string
	fuzzMap     map[string]int64
	fuzzText    string
)

// shapeReader builds data values from the bytes of a fuzz input; it reads zeros once they run out.
type shapeReader struct {
	in []byte
}

// next returns the next byte of the input.
func (r *shapeReader) next() byte {
	if len(r.in) == 0 {
		return 0
	}

	b := r.in[0]
	r.in = r.in[1:]

	return b
}

// count returns a count of 1 to 2^maxLog, chosen by the next byte.
func (r *shapeReader) count(maxLog int) int {
	return 1 << (int(r.next()) % (maxLog + 1))
}

// value returns the value of the shape the next byte selects, nested depth levels deep.
func (r *shapeReader) value(depth int) any {
	if depth >= maxShapeDepth {
		return r.leaf()
	}

	switch r.next() % shapeCount {
	case shapeList:
		list := make([]any, r.count(3))
		for i := range list {
			list[i] = r.value(depth + 1)
		}

		return list
	case shapeMap:
		return r.document(depth, func(keys []string, values []any) any {
			m := map[string]any{}
			for i, k := range keys {
				m[k] = values[i]
			}

			return m
		})
	case shapeDocument:
		return r.document(depth, func(keys []string, values []any) any {
			m := make(map[string]any, len(keys))
			for i, k := range keys {
				m[k] = values[i]
			}

			return m
		})
	case shapeRepeatedKeys:
		list := make([]any, r.count(20))

		key := fuzzShared().key[:r.count(16)]
		for i := range list {
			list[i] = map[string]any{key: int64(i)}
		}

		return list
	case shapeBSONArray:
		return []any{r.value(depth + 1), r.value(depth + 1)}
	case shapeRaw:
		return r.leaf()
	case shapeTypedSlice:
		return r.typedSlice()
	case shapeZeroArray:
		return reflect.New(reflect.ArrayOf(r.count(30), reflect.TypeFor[struct{}]())).Elem().Interface()
	case shapeArray:
		elem := []reflect.Type{reflect.TypeFor[int64](), reflect.TypeFor[string](), reflect.TypeFor[fuzzHook](), reflect.TypeFor[any]()}

		t := reflect.ArrayOf(r.count(12), elem[int(r.next())%len(elem)])
		if r.next()%2 == 0 {
			return reflect.New(t).Interface() // a pointer to the array
		}

		return reflect.New(t).Elem().Interface()
	case shapePointer:
		v := r.value(depth + 1)

		p := &v
		for range r.count(6) {
			q := any(p)
			p = &q
		}

		return p
	case shapeCycle:
		return r.cycle()
	case shapeDAG:
		var dag any = r.leaf()
		for range r.count(5) {
			dag = map[string]any{"a": []any{dag, dag}}
		}

		return dag
	case shapeHooks:
		return r.hooks()
	case shapeDecimal:
		return r.decimal()
	case shapeDecimal128:
		return r.leaf()
	case shapeTypedMap:
		return r.typedMap()
	case shapeStruct:
		return r.structValue(depth)
	case shapeLongString:
		return []any{fuzzShared().long, fuzzText(fuzzShared().long[:r.count(23)]), json.Number(strings.Repeat("9", r.count(20))),
			[]byte(fuzzShared().long[:r.count(16)]), fuzzText(fuzzShared().long)}[r.next()%5]
	case shapeLongList:
		return [][]any{{}, fuzzShared().nulls, fuzzShared().ints}[r.next()%3]
	case shapeWideStruct:
		return reflect.New([]reflect.Type{fuzzShared().wide, fuzzShared().tagged}[r.next()%2]).Elem().Interface()
	case shapeBSONTypes:
		return []any{
			time.Unix(1, 0), struct{ U url.URL }{U: url.URL{Scheme: "https", Host: "h", User: url.User(fuzzShared().long)}},
			&url.URL{Scheme: "https", Host: "h", User: url.UserPassword("a", fuzzShared().key)},
		}[int(r.next())%3]
	default:
		return r.leaf()
	}
}

// leaf returns a scalar.
func (r *shapeReader) leaf() any {
	return []any{
		nil, int64(1), int32(-1), uint64(math.MaxUint64), 1.5, math.NaN(), math.Inf(1), "x", "", true, []byte("x"), int8(1),
		float32(2), uint16(3), fuzzText("x"), decimal.New(1, 0),
	}[int(r.next())%16]
}

// document builds a document of up to maxChildren keys, a long one among them, with build.
func (r *shapeReader) document(depth int, build func(keys []string, values []any) any) any {
	n := int(r.next()) % (maxChildren + 1)

	keys, values := make([]string, n), make([]any, n)
	for i := range n {
		keys[i] = []string{"a", "x", "d", "s", fuzzShared().key, strconv.Itoa(i)}[int(r.next())%6]
		values[i] = r.value(depth + 1)
	}

	return build(keys, values)
}

// typedSlice returns a slice of a type other than []any, up to [maxSliceLength] elements, or many sharing a long string.
func (r *shapeReader) typedSlice() any {
	n := min(r.count(20), maxSliceLength)
	switch r.next() % 6 {
	case 0:
		return make(fuzzInts, n)
	case 1:
		s := make(fuzzStrings, r.count(16))
		for i := range s {
			s[i] = fuzzShared().long
		}

		return s
	case 2:
		return make([]bool, n)
	case 3:
		s := make([]string, n)
		for i := range s {
			s[i] = fuzzShared().key
		}

		return s
	case 4:
		return make([][]byte, n)
	default:
		return make([]fuzzHolder, min(n, 1<<12))
	}
}

// cycle returns a value that reaches itself.
func (r *shapeReader) cycle() any {
	switch r.next() % 4 {
	case 0:
		var x any

		x = &x

		return x
	case 1:
		m := map[string]any{}
		m["a"] = []any{m, m}

		return m
	case 2:
		n := &fuzzNode{Name: "x"}
		n.Next = n

		return n
	default:
		h := &fuzzHolder{S: "x"}
		h.P = h
		h.A = h

		return *h
	}
}

// hooks returns values that encode themselves, alone or many in a container: their output large or not, growing when they are
// asked again, through a pointer to the struct holding them.
func (r *shapeReader) hooks() any {
	n := r.count(12)

	large := r.next()%2 == 0
	switch r.next() % 9 {
	case 0:
		return fuzzHook{Large: large}
	case 1:
		hooks := make([]fuzzHook, n)
		for i := range hooks {
			hooks[i].Large = large
		}

		return struct{ A []fuzzHook }{A: hooks}
	case 2:
		return struct{ A []fuzzDocumentHook }{A: make([]fuzzDocumentHook, n)}
	case 3:
		return struct{ A []fuzzPointerHook }{A: make([]fuzzPointerHook, n)}
	case 4:
		return struct{ P *fuzzPointerHook }{}
	case 5:
		return &fuzzPointerHook{}
	case 6:
		hooks := make([]fuzzGrowingHook, n)
		for i := range hooks {
			hooks[i].calls = new(int)
		}

		return struct{ A []fuzzGrowingHook }{A: hooks}
	case 7:
		return &struct{ A [4]fuzzPointerHook }{}
	default:
		m := make(map[int]fuzzHook, min(n, 64))
		for i := range min(n, 64) {
			m[i] = fuzzHook{Large: large}
		}

		return m
	}
}

// decimal returns a decimal, alone or nested, of a coefficient of up to 200,001 digits or an extreme exponent.
func (r *shapeReader) decimal() any {
	powers := fuzzShared().powers
	digits := []int32{3, 40, 5000, 200_000}
	i := int(r.next()) % len(powers)
	d := decimal.NewFromBigInt(powers[i], -digits[i])

	switch r.next() % 4 {
	case 1:
		d = decimal.New(1, math.MaxInt32)
	case 2:
		d = decimal.New(7, math.MinInt32)
	}

	switch r.next() % 8 {
	case 0:
		return d
	case 1:
		return struct{ D decimal.Decimal }{D: d}
	case 2:
		return struct {
			D optional.Optional[decimal.Decimal]
		}{D: optional.Some(d)}
	case 3:
		return struct{ D decimal.NullDecimal }{D: decimal.NullDecimal{Decimal: d, Valid: true}}
	case 4:
		return &d
	case 5:
		return []decimal.Decimal{d, d}
	case 6:
		return map[decimal.Decimal]int64{decimal.New(1, 1<<24): 1}
	default:
		return fuzzHolder{D: d, A: d}
	}
}

// typedMap returns a map of a type other than map[string]any: long keys, many keys, keys that are not strings.
func (r *shapeReader) typedMap() any {
	switch r.next() % 4 {
	case 0:
		m := fuzzMap{}
		for i := range r.count(7) {
			m[fuzzShared().key+strconv.Itoa(i)] = int64(i)
		}

		return m
	case 1:
		n := r.count(14)

		m := make(map[string]int64, n)
		for i := range n {
			m[strconv.Itoa(i)] = int64(i)
		}

		return m
	case 2:
		return map[int]string{1: fuzzShared().long, 2: "x"}
	default:
		return map[bool]int64{true: 1}
	}
}

// structValue returns a struct: a chain of nodes, one inlining itself, one with a long key, one holding other shapes.
func (r *shapeReader) structValue(depth int) any {
	switch r.next() % 5 {
	case 0:
		var head *fuzzNode
		for range r.count(8) {
			head = &fuzzNode{Name: "x", Next: head}
		}

		return head
	case 1:
		return fuzzInline{}
	case 2:
		return make([]fuzzWide, min(r.count(20), maxSliceLength))
	case 3:
		return struct {
			C chan int
			F func()
		}{}
	default:
		return fuzzHolder{
			A: r.value(depth + 1), S: fuzzShared().long[:r.count(23)], L: []any{r.value(depth + 1)},
			M: map[string]any{"a": r.value(depth + 1)},
		}
	}
}
