// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

type covRow = map[string]any

// TestCoverageEvaluate evaluates every value class, NULL shape and operator in the default and the contract program.
func TestCoverageEvaluate(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	uuid := "123e4567-e89b-12d3-a456-426614174000"

	for _, tc := range []struct {
		name     string
		expr     string
		node     filter.Node
		row      covRow
		contract bool
		want     bool
		wantErr  error
	}{
		// Scalars of every kind and Go representation.
		{name: "int32 value", expr: `i == 3`, row: covRow{"i": int32(3)}, want: true},
		{name: "uint16 value", expr: `i == 3`, row: covRow{"i": uint16(3)}, want: true},
		{name: "uint64 value", expr: `i == 3`, row: covRow{"i": uint64(3)}, want: true},
		{name: "pointer value", expr: `i == 3`, row: covRow{"i": new(int64(3))}, want: true},
		{name: "nil pointer", expr: `i == null`, row: covRow{"i": (*int64)(nil)}, want: true},
		{name: "float int", expr: `i == 3`, row: covRow{"i": 3.0}, want: true},
		{name: "float fraction", expr: `i == 3`, row: covRow{"i": 3.5}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "int overflow", expr: `i == 3`, row: covRow{"i": "99999999999999999999"}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "int not a number", expr: `i == 3`, row: covRow{"i": true}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "float32", expr: `f == 1.5`, row: covRow{"f": float32(1.5)}, want: true},
		{name: "float from text", expr: `f == 1.5`, row: covRow{"f": "1.5"}, want: true},
		{name: "float from int", expr: `f == 2.0`, row: covRow{"f": int64(2)}, want: true},
		{name: "float from int8", expr: `f == 2.0`, row: covRow{"f": int8(2)}, want: true},
		{name: "float overflow", expr: `f == 1.5`, row: covRow{"f": "1e400"}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "float underflow", expr: `f == 1.5`, row: covRow{"f": "1e-400"}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "float NaN text", expr: `f > 1.5`, row: covRow{"f": "NaN"}, want: true},
		{name: "float NaN equal", node: bin(filter.OpEqual, ident("f"), lit(math.NaN())), row: covRow{"f": math.NaN()}, want: true},
		{name: "float below NaN", node: bin(filter.OpLT, ident("f"), lit(math.NaN())), row: covRow{"f": 1.0}, want: true},
		{name: "float infinity", expr: `f < 1.5`, row: covRow{"f": "-Infinity"}, want: true},
		{name: "float plus infinity", expr: `f > 1.5`, row: covRow{"f": "+inf"}, want: true},
		{name: "float bad", expr: `f > 1.5`, row: covRow{"f": "x"}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "float unsupported type", expr: `f > 1.5`, row: covRow{"f": struct{}{}}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "numeric decimal", expr: `n == "1.5"`, row: covRow{"n": decimal.RequireFromString("1.50")}, want: true},
		{name: "numeric json number", expr: `n == "1.5"`, row: covRow{"n": json.Number("1.5")}, want: true},
		{name: "numeric float", expr: `n < "2"`, row: covRow{"n": 1.5}, want: true},
		{name: "numeric uint", expr: `n < "2"`, row: covRow{"n": uint(1)}, want: true},
		{name: "numeric huge exponent", expr: `n < "2"`, row: covRow{"n": decimal.New(1, 200000)}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "numeric too many digits", expr: `n < "2"`, row: covRow{"n": "1e200000"}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "numeric bad", expr: `n < "2"`, row: covRow{"n": "abc"}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "numeric minus infinity", expr: `n < "2"`, row: covRow{"n": "-Infinity"}, want: true},
		{name: "numeric both infinite", expr: `n == "Infinity"`, row: covRow{"n": "inf"}, want: true},
		{name: "numeric NaN below", expr: `n < "NaN"`, row: covRow{"n": "Infinity"}, want: true},
		{name: "bool true", expr: `b`, row: covRow{"b": true}, want: true},
		{name: "bool compare", expr: `b > false`, row: covRow{"b": true}, want: true},
		{name: "bool lesser", expr: `b < true`, row: covRow{"b": false}, want: true},
		{name: "bool wrong type", expr: `b`, row: covRow{"b": "true"}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "text wrong type", expr: `s == "a"`, row: covRow{"s": 1}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "text bytes", expr: `s == "a"`, row: covRow{"s": []byte("a")}, want: true},
		{name: "text NUL", expr: `s == "a"`, row: covRow{"s": "a\x00"}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "text invalid UTF-8", expr: `s == "a"`, row: covRow{"s": "\xff"}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "qualified nested", expr: `q.c == "a"`, row: covRow{"q": covRow{"c": "a"}}, want: true},
		{name: "qualified missing", expr: `q.c == null`, row: covRow{"q": covRow{}}, want: true},
		{name: "qualified not a map", expr: `q.c == null`, row: covRow{"q": 1}, want: true},
		{name: "bytes compare", node: bin(filter.OpLT, ident("by"), lit([]byte{2})), row: covRow{"by": []byte{1}}, want: true},
		{name: "bytes wrong type", node: bin(filter.OpLT, ident("by"), lit([]byte{2})), row: covRow{"by": "x"}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "timestamp compare", node: bin(filter.OpGT, ident("ts"), lit(ts)), row: covRow{"ts": ts.Add(time.Second)}, want: true},
		{name: "timestamp wrong type", node: bin(filter.OpGT, ident("ts"), lit(ts)), row: covRow{"ts": "x"}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "uuid text", expr: `u == "` + uuid + `"`, row: covRow{"u": "{123E4567E89B12D3A456426614174000}"}, want: true},
		{name: "uuid bytes", expr: `u == "` + uuid + `"`, row: covRow{"u": [16]byte{0x12, 0x3e, 0x45, 0x67, 0xe8, 0x9b, 0x12, 0xd3, 0xa4, 0x56, 0x42, 0x66, 0x14, 0x17, 0x40, 0x00}}, want: true},
		{name: "uuid invalid", expr: `u == "` + uuid + `"`, row: covRow{"u": "nope"}, wantErr: filter.ErrFieldTypeMismatch},

		// NULL semantics.
		{name: "ordered null unknown", expr: `!(i > 1)`, row: covRow{"i": nil}, want: false},
		{name: "computed ne field null", node: bin(filter.OpNotEqual, call(filter.OpSize, ident("s")), ident("pi")), row: covRow{"s": nil, "pi": nil}, want: false},
		{name: "computed eq null", node: bin(filter.OpEqual, call(filter.OpSize, ident("s")), lit(nil)), row: covRow{"s": nil}, want: false},
		{name: "computed ne null", node: not(bin(filter.OpEqual, call(filter.OpSize, ident("s")), lit(nil))), row: covRow{"s": nil}, want: false},
		{name: "string call null argument", expr: `s.contains(ps)`, row: covRow{"s": "a", "ps": nil}, want: false},
		{name: "string call wrong target", expr: `s.contains("a")`, row: covRow{"s": 1}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "starts with", expr: `s.startsWith("a")`, row: covRow{"s": "ab"}, want: true},
		{name: "ends with", expr: `s.endsWith("b")`, row: covRow{"s": "ab"}, want: true},
		{name: "matches", expr: `s.matches("^a")`, row: covRow{"s": "ab"}, want: true},
		{name: "not bool null", expr: `!b`, row: covRow{"b": nil}, want: false},
		{name: "and false", expr: `b && false`, row: covRow{"b": nil}, want: false},
		{name: "and unknown", expr: `!(b && true)`, row: covRow{"b": nil}, want: false},
		{name: "or unknown", expr: `!(b || false)`, row: covRow{"b": nil}, want: false},
		{name: "or false", expr: `!(b || false)`, row: covRow{"b": false}, want: true},
		{name: "in unknown leaf", node: bin(filter.OpIn, ident("i"), list(lit(int64(1)), call(filter.OpSize, ident("s")))), row: covRow{"i": int64(2), "s": nil}, want: false},
		{name: "null in null column", node: bin(filter.OpIn, ident("pi"), list(lit(int64(1)), ident("i"))), row: covRow{"pi": nil, "i": nil}, want: true},
		{name: "null in skipped", node: bin(filter.OpIn, ident("pi"), list(lit(int64(1)))), row: covRow{"pi": nil}, want: false},
		{name: "in array column", node: bin(filter.OpIn, ident("i"), ident("ai")), row: covRow{"i": int64(2), "ai": []int64{1, 2}}, want: true},
		{name: "null in array column", node: bin(filter.OpIn, ident("pi"), ident("ai")), row: covRow{"pi": nil, "ai": []int64{1}}, want: false},
		{name: "null in empty array", node: not(bin(filter.OpIn, ident("pi"), ident("ai"))), row: covRow{"pi": nil, "ai": []int64{}}, want: true},
		{name: "computed in", node: bin(filter.OpIn, call(filter.OpSize, ident("s")), list(lit(int64(1)))), row: covRow{"s": nil}, want: false},

		// Arrays.
		{name: "array element ne", expr: `ai != 3`, row: covRow{"ai": []int64{1, 2}}, want: true},
		{name: "array element gt", expr: `ai > 1`, row: covRow{"ai": []any{nil, int64(2)}}, want: true},
		{name: "array null computed", node: bin(filter.OpEqual, ident("ai"), call(filter.OpSize, ident("s"))), row: covRow{"ai": []int64{1}, "s": nil}, want: false},
		{name: "array null scalar", node: bin(filter.OpEqual, ident("ai"), ident("pi")), row: covRow{"ai": []int64{1}, "pi": nil}, want: false},
		{name: "array equal list", expr: `ai == [1, 2]`, row: covRow{"ai": []int{1, 2}}, want: true},
		{name: "array shorter", expr: `ai < [1, 2]`, row: covRow{"ai": []int{1}}, want: true},
		{name: "array null elements", expr: `ai > [1, 2]`, row: covRow{"ai": []any{nil, nil}}, want: true},
		{name: "array null elements equal", expr: `ai == [null, 2]`, row: covRow{"ai": []any{nil, 2}}, want: true},
		{name: "array null right element", expr: `ai < [null]`, row: covRow{"ai": []any{1}}, want: true},
		{name: "array nested", expr: `ai == [[1, 2], [3, 4]]`, row: covRow{"ai": [][]int64{{1, 2}, {3, 4}}}, want: true},
		{name: "array nested shorter dims", expr: `ai > [1, 2, 3, 4]`, row: covRow{"ai": [][]int64{{1, 2}, {3, 4}}}, want: true},
		{name: "array ragged", expr: `ai == [1]`, row: covRow{"ai": [][]int64{{1, 2}, {3}}}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "array mixed depth", expr: `ai == [1]`, row: covRow{"ai": []any{[]int64{1}, int64(2)}}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "array not a slice", expr: `ai == [1]`, row: covRow{"ai": 1}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "array numeric coercion", node: bin(filter.OpEqual, ident("ai"), ident("an")), row: covRow{"ai": []int64{1}, "an": []string{"1.0"}}, want: true},
		{name: "array numeric coercion reversed", node: bin(filter.OpEqual, ident("an"), ident("ai")), row: covRow{"ai": []int64{1}, "an": []string{"1.0"}}, want: true},
		{name: "array float coercion", node: bin(filter.OpEqual, ident("af"), ident("ai")), row: covRow{"ai": []int64{1}, "af": []float64{1}}, want: true},
		{name: "array bytes", node: bin(filter.OpEqual, ident("ab"), lit([]byte("a"))), row: covRow{"ab": [][]byte{[]byte("a")}}, want: true},
		{name: "array uuid", expr: `au == "` + uuid + `"`, row: covRow{"au": [][16]byte{{0x12, 0x3e, 0x45, 0x67, 0xe8, 0x9b, 0x12, 0xd3, 0xa4, 0x56, 0x42, 0x66, 0x14, 0x17, 0x40, 0x00}}}, want: true},
		{name: "array json", expr: `aj == ['{"a":1}']`, row: covRow{"aj": []string{`{"a": 1}`}}, want: true},
		{name: "array bool", expr: `abo == true`, row: covRow{"abo": []bool{false, true}}, want: true},
		{name: "array string call", expr: `tx.endsWith("z")`, row: covRow{"tx": []any{nil, "az"}}, want: true},
		{name: "array string call none", expr: `tx.startsWith("z")`, row: covRow{"tx": []string{"a"}}, want: false},
		{name: "array string call null array", expr: `tx.startsWith("z")`, row: covRow{"tx": nil}, want: false},
		{name: "array string call null arg", expr: `tx.startsWith(ps)`, row: covRow{"tx": []string{"a"}, "ps": nil}, want: false},
		{name: "array string call wrong element", expr: `tx.startsWith("z")`, row: covRow{"tx": []any{1}}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "array size", expr: `size(ai) == 2`, row: covRow{"ai": []int64{1, 2}}, want: true},
		{name: "list size", expr: `size(["a", "b"]) == 2`, row: covRow{}, want: true},
		{name: "explicit array", expr: `ai == [1, 2]`, row: covRow{"ai": postgres.ArrayData{Elements: []any{1, 2}, Dimensions: []postgres.Dimension{{Length: 2, LowerBound: 1}}, Valid: true}}, want: true},
		{name: "explicit array lower bound", expr: `ai > [1, 2]`, row: covRow{"ai": postgres.ArrayData{Elements: []any{1, 2}, Dimensions: []postgres.Dimension{{Length: 2, LowerBound: 2}}, Valid: true}}, want: true},
		{name: "explicit array dims", expr: `ai > [1, 2]`, row: covRow{"ai": postgres.ArrayData{Elements: []any{1, 2}, Dimensions: []postgres.Dimension{{Length: 1, LowerBound: 1}, {Length: 2, LowerBound: 1}}, Valid: true}}, want: true},
		{name: "explicit array lengths", expr: `ai < [[1, 2]]`, row: covRow{"ai": postgres.ArrayData{Elements: []any{1, 2}, Dimensions: []postgres.Dimension{{Length: 2, LowerBound: 1}, {Length: 1, LowerBound: 1}}, Valid: true}}, want: false},
		{name: "explicit empty", expr: `ai == []`, row: covRow{"ai": postgres.ArrayData{Valid: true}}, want: true},
		{name: "explicit null", expr: `ai == null`, row: covRow{"ai": postgres.ArrayData{}}, want: true},
		{name: "explicit bad total", expr: `ai == []`, row: covRow{"ai": postgres.ArrayData{Elements: []any{1}, Dimensions: []postgres.Dimension{{Length: 2, LowerBound: 1}}, Valid: true}}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "explicit missing dims", expr: `ai == []`, row: covRow{"ai": postgres.ArrayData{Elements: []any{1}, Valid: true}}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "explicit negative length", expr: `ai == []`, row: covRow{"ai": postgres.ArrayData{Dimensions: []postgres.Dimension{{Length: -1, LowerBound: 1}}, Valid: true}}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "explicit lower bound overflow", expr: `ai == []`, row: covRow{"ai": postgres.ArrayData{Dimensions: []postgres.Dimension{{Length: 0, LowerBound: math.MaxInt32 + 2}}, Valid: true}}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "explicit too many dims", expr: `ai == []`, row: covRow{"ai": postgres.ArrayData{Dimensions: make([]postgres.Dimension, 7), Valid: true}}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "explicit bad element", expr: `ai == []`, row: covRow{"ai": postgres.ArrayData{Elements: []any{"x"}, Dimensions: []postgres.Dimension{{Length: 1, LowerBound: 1}}, Valid: true}}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "array empty nested", expr: `ai == []`, row: covRow{"ai": [][][][][][][]int64{}}, want: true},
		{name: "array element error", expr: `ai == []`, row: covRow{"ai": []any{"x"}}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "array nested element error", expr: `ai == []`, row: covRow{"ai": []any{[]any{"x"}}}, wantErr: filter.ErrFieldTypeMismatch},

		// JSON.
		{name: "json raw message", expr: `j == '{"a":1}'`, row: covRow{"j": json.RawMessage(`{"a":1}`)}, want: true},
		{name: "json bytes", expr: `j == '{"a":1}'`, row: covRow{"j": []byte(`{"a":1}`)}, want: true},
		{name: "json native map", expr: `j == '{"a":[1,"x",true,null]}'`, row: covRow{"j": map[string]any{"a": []any{int64(1), "x", true, nil}}}, want: true},
		{name: "json native numbers", expr: `j == '[1,2,3,4]'`, row: covRow{"j": []any{1, 2.0, uint64(3), decimal.NewFromInt(4)}}, want: true},
		{name: "json native unsupported", expr: `j == '[]'`, row: covRow{"j": []any{struct{}{}}}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json native NaN", expr: `j == '[]'`, row: covRow{"j": []any{math.NaN()}}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json native bad key", expr: `j == '{}'`, row: covRow{"j": map[string]any{"\xff": 1}}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json invalid", expr: `j == '{}'`, row: covRow{"j": `{`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json trailing", expr: `j == '{}'`, row: covRow{"j": `{} {}`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json invalid UTF-8", expr: `j == '{}'`, row: covRow{"j": "\"\xff\""}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json lone low surrogate", expr: `j == '{}'`, row: covRow{"j": `"\udc00"`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json unpaired high surrogate", expr: `j == '{}'`, row: covRow{"j": `"\ud800x"`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json bad low surrogate", expr: `j == '{}'`, row: covRow{"j": `"\ud800A"`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json short escape", expr: `j == '{}'`, row: covRow{"j": `"\u00"`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json bad hex", expr: `j == '{}'`, row: covRow{"j": `"\uzzzz"`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json trailing backslash", expr: `j == '{}'`, row: covRow{"j": `"\`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json surrogate pair", expr: `j == '"😀"'`, row: covRow{"j": `"😀"`}, want: true},
		{name: "json escapes", expr: `j == '"a\\nb"'`, row: covRow{"j": `"a\nb"`}, want: true},
		{name: "json array order", expr: `j < '[1, 2]'`, row: covRow{"j": `[3]`}, want: true},
		{name: "json empty array first", expr: `j < 'null'`, row: covRow{"j": `[]`}, want: true},
		{name: "json empty array right", expr: `j > '[]'`, row: covRow{"j": `null`}, want: true},
		{name: "json rank", expr: `j > '"a"'`, row: covRow{"j": `1`}, want: true},
		{name: "json strings", expr: `j > '"a"'`, row: covRow{"j": `"b"`}, want: true},
		{name: "json bools", expr: `j > 'false'`, row: covRow{"j": `true`}, want: true},
		{name: "json nulls", expr: `j >= 'null'`, row: covRow{"j": `null`}, want: true},
		{name: "json lists", expr: `j > '[1, 2]'`, row: covRow{"j": `[1, 3]`}, want: true},
		{name: "json objects size", expr: `j > '{"a": 1}'`, row: covRow{"j": `{"a": 1, "b": 2}`}, want: true},
		{name: "json objects keys", expr: `j > '{"a": 1}'`, row: covRow{"j": `{"b": 1}`}, want: true},
		{name: "json objects values", expr: `j > '{"a": 1}'`, row: covRow{"j": `{"a": 2}`}, want: true},
		{name: "json objects equal", expr: `j == '{"a": 1}'`, row: covRow{"j": `{"a": 1.0}`}, want: true},
		{name: "json size", expr: `size(j) == 2`, row: covRow{"j": `[1, 2]`}, want: true},
		{name: "json size object", expr: `size(j) == 2`, row: covRow{"j": `{}`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json size null", expr: `size(j) == 2`, row: covRow{"j": nil}, want: false},
		{name: "json contains null target", expr: `j.contains('1')`, row: covRow{"j": nil}, want: false},
		{name: "json contains null arg", node: call(filter.OpContains, ident("j"), ident("jp.j")), row: covRow{"j": `[1]`, "doc": `{}`}, want: false},
		{name: "json contains object mismatch", expr: `j.contains('{"a":1}')`, row: covRow{"j": `[1]`}, want: false},
		{name: "json contains missing key", expr: `j.contains('{"a":1}')`, row: covRow{"j": `{"b":1}`}, want: false},
		{name: "json contains different value", expr: `j.contains('{"a":1}')`, row: covRow{"j": `{"a":2}`}, want: false},
		{name: "json contains array mismatch", expr: `j.contains('[1]')`, row: covRow{"j": `{"a":1}`}, want: false},
		{name: "json contains missing element", expr: `j.contains('[1]')`, row: covRow{"j": `[2]`}, want: false},
		{name: "json contains scalar in array", expr: `j.contains('1')`, row: covRow{"j": `[1]`}, want: true},
		{name: "json contains scalar", expr: `j.contains('"a"')`, row: covRow{"j": `"a"`}, want: true},
		{name: "json number literal", node: bin(filter.OpEqual, ident("j"), &filter.LiteralNode{Value: 1.5, Text: "1.50"}), row: covRow{"j": `1.5`}, want: true},
		{name: "json bad number literal", node: bin(filter.OpEqual, ident("j"), &filter.LiteralNode{Value: 1.5, Text: "x"}), row: covRow{"j": `1.5`}, wantErr: filter.ErrFieldTypeMismatch},

		// JSON paths.
		{name: "json path int", expr: `jp.i == 3`, row: covRow{"doc": `{"i": 3}`}, want: true},
		{name: "json path int text", expr: `jp.i == 3`, row: covRow{"doc": `{"i": " 3"}`}, want: true},
		{name: "json path int bad", expr: `jp.i == 3`, row: covRow{"doc": `{"i": 3.5}`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json path null", expr: `jp.i == null`, row: covRow{"doc": `{"i": null}`}, want: true},
		{name: "json path missing", expr: `jp.i == null`, row: covRow{"doc": `{}`}, want: true},
		{name: "json path not object", expr: `jp.i == null`, row: covRow{"doc": `1`}, want: true},
		{name: "json path array index", expr: `jp.a == "z"`, row: covRow{"doc": `{"a": ["x", "z"]}`}, want: true},
		{name: "json path array out of range", expr: `jp.a == null`, row: covRow{"doc": `{"a": []}`}, want: true},
		{name: "json path array bad index", expr: `jp.o == null`, row: covRow{"doc": `{"o": [1]}`}, want: true},
		{name: "json path object", expr: `jp.o == "y"`, row: covRow{"doc": `{"o": {"x": "y"}}`}, want: true},
		{name: "json path text of object", expr: `jp.s == '{"a": 1}'`, row: covRow{"doc": `{"s": {"a": 1}}`}, want: true},
		{name: "json path text of number", expr: `jp.s == "1.50"`, row: covRow{"doc": `{"s": 1.50}`}, want: true},
		{name: "json path text of integer", expr: `jp.s == "15"`, row: covRow{"doc": `{"s": 15}`}, want: true},
		{name: "json path text of list", expr: `jp.s == '["a\\u0001", true, null]'`, row: covRow{"doc": `{"s": ["a\u0001", true, null]}`}, want: true},
		{name: "json path text of escapes", expr: `jp.s == '["\\"\\\\\\b\\f\\n\\r\\t"]'`, row: covRow{"doc": `{"s": ["\"\\\b\f\n\r\t"]}`}, want: true},
		{name: "json path bool true", expr: `jp.b`, row: covRow{"doc": `{"b": true}`}, want: true},
		{name: "json path bool yes", expr: `jp.b`, row: covRow{"doc": `{"b": "Ye"}`}, want: true},
		{name: "json path bool on", expr: `jp.b`, row: covRow{"doc": `{"b": "on"}`}, want: true},
		{name: "json path bool off", expr: `!jp.b`, row: covRow{"doc": `{"b": "of"}`}, want: true},
		{name: "json path bool zero", expr: `!jp.b`, row: covRow{"doc": `{"b": "0"}`}, want: true},
		{name: "json path bool no", expr: `!jp.b`, row: covRow{"doc": `{"b": "n"}`}, want: true},
		{name: "json path bool empty", expr: `jp.b`, row: covRow{"doc": `{"b": " "}`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json path bool bad", expr: `jp.b`, row: covRow{"doc": `{"b": "o"}`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json path bytes hex", node: bin(filter.OpEqual, ident("jp.y"), lit([]byte{0x0a, 0xbc})), row: covRow{"doc": `{"y": "\\x0a BC"}`}, want: true},
		{name: "json path bytes hex odd", node: bin(filter.OpEqual, ident("jp.y"), lit([]byte{0x0a})), row: covRow{"doc": `{"y": "\\x0"}`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json path bytes hex bad", node: bin(filter.OpEqual, ident("jp.y"), lit([]byte{0x0a})), row: covRow{"doc": `{"y": "\\xzz"}`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json path bytes escape", node: bin(filter.OpEqual, ident("jp.y"), lit([]byte("a\\\x01"))), row: covRow{"doc": `{"y": "a\\\\\\001"}`}, want: true},
		{name: "json path bytes trailing", node: bin(filter.OpEqual, ident("jp.y"), lit([]byte("a"))), row: covRow{"doc": `{"y": "a\\"}`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json path bytes short octal", node: bin(filter.OpEqual, ident("jp.y"), lit([]byte("a"))), row: covRow{"doc": `{"y": "\\01"}`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json path bytes bad octal", node: bin(filter.OpEqual, ident("jp.y"), lit([]byte("a"))), row: covRow{"doc": `{"y": "\\400"}`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json path timestamp", node: bin(filter.OpEqual, ident("jp.t"), lit(ts)), row: covRow{"doc": `{"t": "2026-01-02T03:04:05Z"}`}, want: true},
		{name: "json path timestamp fraction", node: bin(filter.OpGT, ident("jp.t"), lit(ts)), row: covRow{"doc": `{"t": "2026-01-02T03:04:05.0000015Z"}`}, want: true},
		{name: "json path timestamp bad", node: bin(filter.OpEqual, ident("jp.t"), lit(ts)), row: covRow{"doc": `{"t": "x"}`}, wantErr: filter.ErrFieldTypeMismatch},
		{name: "json path float", expr: `jp.f == 1.5`, row: covRow{"doc": `{"f": 1.5}`}, want: true},
		{name: "json path numeric", expr: `jp.n == "1.5"`, row: covRow{"doc": `{"n": 1.5}`}, want: true},
		{name: "json path json", expr: `jp.j == '[1]'`, row: covRow{"doc": `{"j": [1]}`}, want: true},
		{name: "json path from map", expr: `jp.i == 3`, row: covRow{"doc": map[string]any{"i": 3}}, want: true},

		// Contract mode.
		{name: "contract zero int", expr: `i == 0`, row: covRow{"i": nil}, contract: true, want: true},
		{name: "contract zero array", expr: `size(ai) == 0`, row: covRow{"ai": nil}, contract: true, want: true},
		{name: "contract uuid null", expr: `u == null`, row: covRow{"u": nil}, contract: true, want: false},
		{name: "contract uuid not null", expr: `u != null`, row: covRow{"u": nil}, contract: true, want: true},
		{name: "contract has scalar", node: call(filter.OpHas, ident("i")), row: covRow{"i": int64(1)}, contract: true, want: true},
		{name: "contract has zero", node: call(filter.OpHas, ident("i")), row: covRow{"i": int64(0)}, contract: true, want: false},
		{name: "contract has array", node: call(filter.OpHas, ident("ai")), row: covRow{"ai": []int64{1}}, contract: true, want: true},
		{name: "contract has empty array", node: call(filter.OpHas, ident("ai")), row: covRow{"ai": []int64{}}, contract: true, want: false},
		{name: "contract has uuid", node: call(filter.OpHas, ident("u")), row: covRow{"u": uuid}, contract: true, want: true},
		{name: "contract has presence", node: call(filter.OpHas, ident("ps")), row: covRow{"ps": nil}, contract: true, want: false},
		{name: "contract size presence", expr: `size(ps) == 0`, row: covRow{"ps": nil}, contract: true, want: true},
		{name: "contract eq null scalar", expr: `i == null`, row: covRow{"i": int64(0)}, contract: true, want: true},
		{name: "contract ne null scalar", expr: `s != null`, row: covRow{"s": "a"}, contract: true, want: true},
		{name: "contract eq null array", expr: `ai == null`, row: covRow{"ai": []int64{}}, contract: true, want: true},
		{name: "contract ne null array", expr: `ai != null`, row: covRow{"ai": []int64{1}}, contract: true, want: true},
		{name: "contract eq null timestamp", expr: `ts == null`, row: covRow{"ts": time.Time{}}, contract: true, want: true},
		{name: "contract eq null bytes", expr: `by == null`, row: covRow{"by": []byte{}}, contract: true, want: true},
		{name: "contract eq null float", expr: `f == null`, row: covRow{"f": 0.0}, contract: true, want: true},
		{name: "contract eq null bool", expr: `b == null`, row: covRow{"b": false}, contract: true, want: true},
		{name: "contract eq null numeric", expr: `n == null`, row: covRow{"n": "0"}, contract: true, want: true},
		{name: "contract array in list", expr: `ai in [3, 2]`, row: covRow{"ai": []any{nil, int64(2)}}, contract: true, want: true},
		{name: "contract array in list null", node: bin(filter.OpIn, ident("ai"), list(lit(nil), lit(int64(5)))), row: covRow{"ai": []int64{2}}, contract: true, want: false},
		{name: "contract array in empty", expr: `ai in [3]`, row: covRow{"ai": nil}, contract: true, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			node := tc.node
			if node == nil {
				node = filtertest.MustParseFilter(t, tc.expr)
			}

			s := covSchema(t, postgres.WithSchemaJSONBStringCompare(strings.Compare))

			p, err := s.Compile(covPolicy(t), node)
			if tc.contract {
				p, err = s.CompileContract(covPolicy(t), node)
			}

			if err != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}

			got, err := p.Evaluate(tc.row, postgres.DefaultMaxDataBudget, nil)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

type covDecoder func(any) (any, error)

func (d covDecoder) Decode(v any) (any, error) { return d(v) }

type covWrapped struct{ v any }

// TestCoverageEvaluateDecoder unwraps driver values through a decoder, which may change the type, return SQL NULL or fail.
func TestCoverageEvaluateDecoder(t *testing.T) {
	t.Parallel()

	errDecode := errors.New("decode")

	for _, tc := range []struct {
		name    string
		decoder covDecoder
		value   any
		want    bool
		wantErr error
	}{
		{"unwraps", func(v any) (any, error) {
			if w, ok := v.(covWrapped); ok {
				return w.v, nil
			}

			return v, nil
		}, covWrapped{int64(1)}, true, nil},
		{"same type pointer", func(v any) (any, error) { return v, nil }, new(int64(1)), true, nil},
		{"null", func(any) (any, error) { return nil, nil }, covWrapped{int64(1)}, false, nil},
		{"error", func(any) (any, error) { return nil, errDecode }, covWrapped{int64(1)}, false, errDecode},
		{"endless", func(v any) (any, error) { return new(v), nil }, int64(1), false, filter.ErrDataLimitExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := covCompile(t, covSchema(t), filtertest.MustParseFilter(t, `i == 1`), false)
			got, err := p.Evaluate(covRow{"i": tc.value}, postgres.DefaultMaxDataBudget, tc.decoder)
			require.ErrorIs(t, err, tc.wantErr)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestCoverageEvaluateLimits stops evaluation at the work budget, the value depth and the array dimension limit.
func TestCoverageEvaluateLimits(t *testing.T) {
	t.Parallel()

	deep := any(int64(1))
	for range 5 {
		deep = &deep
	}

	nestedJSON := strings.Repeat("[", 10) + strings.Repeat("]", 10)

	for _, tc := range []struct {
		name   string
		opts   []postgres.SchemaOption
		expr   string
		row    covRow
		budget int
	}{
		{"budget", nil, `s == "a"`, covRow{"s": "a"}, 2},
		{"budget string call", nil, `s.contains("abc")`, covRow{"s": strings.Repeat("a", 64)}, 10},
		{"budget compare", nil, `s < "abc"`, covRow{"s": strings.Repeat("a", 64)}, 70},
		{"budget json", nil, `j == '[1]'`, covRow{"j": `[1, 2, 3, 4, 5, 6, 7, 8]`}, 12},
		{"budget json strings", nil, `j > '"aaaa"'`, covRow{"j": `"aaaaaaaaaa"`}, 25},
		{"budget json objects", nil, `j > '{"aaaa": 1}'`, covRow{"j": `{"bbbb": 1}`}, 30},
		{"budget numeric", nil, `n == "1.5"`, covRow{"n": "123456789.123456789"}, 30},
		{"budget array", nil, `ai == [1]`, covRow{"ai": make([]int64, 64)}, 30},
		{"budget explicit array", nil, `ai == [1]`, covRow{"ai": postgres.ArrayData{Elements: make([]any, 64), Dimensions: []postgres.Dimension{{Length: 64, LowerBound: 1}}, Valid: true}}, 30},
		{"budget bytes", nil, `size(by) == 1`, covRow{"by": make([]byte, 64)}, 30},
		{"budget json path", nil, `jp.o == "a"`, covRow{"doc": `{"o":{"x":"a"}}`}, 20},
		{"value depth", []postgres.SchemaOption{postgres.WithSchemaMaxValueDepth(3)}, `i == 1`, covRow{"i": deep}, postgres.DefaultMaxDataBudget},
		{"json depth", []postgres.SchemaOption{postgres.WithSchemaMaxValueDepth(3)}, `j == '[]'`, covRow{"j": nestedJSON}, postgres.DefaultMaxDataBudget},
		{"array dimensions", []postgres.SchemaOption{postgres.WithSchemaMaxArrayDimensions(1)}, `ai == [1]`, covRow{"ai": [][]int64{{1}}}, postgres.DefaultMaxDataBudget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := covSchema(t, append(tc.opts, postgres.WithSchemaJSONBStringCompare(strings.Compare))...)
			p := covCompile(t, s, filtertest.MustParseFilter(t, tc.expr), false)
			_, err := p.Evaluate(tc.row, tc.budget, nil)
			require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
		})
	}
}
