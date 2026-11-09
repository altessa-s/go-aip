// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation_test

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/listing/evaluation"
)

type key string

type point struct{ X int }

type selfEncoded struct{ V string }

type wrapped struct{ V string }

type vanishing struct{}

// testEncoding stores wrapped values as their string, encodes point structs as maps and *selfEncoded as its string.
type testEncoding struct{}

func (testEncoding) Stored(v any, _ evaluator.Budget) (any, bool) {
	if w, ok := v.(wrapped); ok {
		return w.V, true
	}

	return v, false
}

func (testEncoding) Encode(v any, _ evaluator.Budget) any {
	switch x := v.(type) {
	case point:
		return map[string]any{"x": int64(x.X)}
	case *point:
		return map[string]any{"x": int64(x.X), "addressed": true}
	case *vanishing:
		return nil
	case *selfEncoded:
		return "self:" + x.V
	default:
		return v
	}
}

func (testEncoding) EncodesItself(t reflect.Type) bool { return t == reflect.TypeFor[*selfEncoded]() }

func (testEncoding) Owns(t reflect.Type) bool { return t == reflect.TypeFor[wrapped]() }

type refusingBudget struct{}

func (refusingBudget) Charge(int) bool { return false }

func TestCoverageReaderLookup(t *testing.T) {
	t.Parallel()

	var nilIface any

	nested := map[string]any{
		"str":     "a",
		"ptr":     new("p"),
		"nilPtr":  (*string)(nil),
		"nilIf":   &nilIface,
		"typed":   map[key]any{"k": "v"},
		"strings": map[string]string{"k": "v"},
		"intKeys": map[int]any{1: "v"},
		"items":   []any{map[string]any{"name": "x"}, map[string]any{"other": 1}, map[string]any{"name": "y"}},
		"array":   [2]map[string]any{{"name": "a"}, {"name": "b"}},
		"none":    []any{map[string]any{"other": 1}},
		"null":    nil,
		"scalar":  int64(1),
		"inner":   map[string]any{"deep": map[string]any{"leaf": true}},
		"wrapped": wrapped{V: "w"},
		"point":   point{X: 3},
		"self":    &selfEncoded{V: "s"},
		"ptrPt":   &point{X: 4},
		"ptrWrap": &wrapped{V: "pw"},
		"ptrTime": &coverageTime,
		"ptrNil":  &vanishing{},
	}

	for _, tc := range []struct {
		name        string
		encoding    evaluator.ValueEncoding
		path        string
		want        any
		wantPresent bool
	}{
		{name: "top level", path: "str", want: "a", wantPresent: true},
		{name: "missing", path: "absent", want: nil, wantPresent: false},
		{name: "pointer", path: "ptr", want: "p", wantPresent: true},
		{name: "nil pointer leaf", path: "nilPtr", want: nil, wantPresent: true},
		{name: "nil pointer mid path", path: "nilPtr.x", want: nil, wantPresent: false},
		{name: "nil interface leaf", path: "nilIf", want: nil, wantPresent: true},
		{name: "null leaf", path: "null", want: nil, wantPresent: true},
		{name: "null mid path", path: "null.x", want: nil, wantPresent: false},
		{name: "named key map", path: "typed.k", want: "v", wantPresent: true},
		{name: "string map", path: "strings.k", want: "v", wantPresent: true},
		{name: "non-string keys", path: "intKeys.1", want: nil, wantPresent: false},
		{name: "slice fan-out", path: "items.name", want: []any{"x", "y"}, wantPresent: true},
		{name: "array fan-out", path: "array.name", want: []any{"a", "b"}, wantPresent: true},
		{name: "slice without match", path: "none.name", want: []any(nil), wantPresent: false},
		{name: "scalar mid path", path: "scalar.x", want: nil, wantPresent: false},
		{name: "nested", path: "inner.deep.leaf", want: true, wantPresent: true},
		{name: "opaque struct", path: "point.x", want: nil, wantPresent: false},
		{name: "stored by encoding", encoding: testEncoding{}, path: "wrapped", want: "w", wantPresent: true},
		{name: "struct encoded", encoding: testEncoding{}, path: "point.x", want: int64(3), wantPresent: true},
		{name: "pointer encodes itself", encoding: testEncoding{}, path: "self", want: "self:s", wantPresent: true},
		{name: "pointer to struct read through", encoding: testEncoding{}, path: "ptrPt.x", want: int64(4), wantPresent: true},
		{name: "pointer to struct encoded from its address", encoding: testEncoding{}, path: "ptrPt.addressed", want: true, wantPresent: true},
		{name: "pointer to owned type stored", encoding: testEncoding{}, path: "ptrWrap", want: "pw", wantPresent: true},
		{name: "pointer to timestamp", encoding: testEncoding{}, path: "ptrTime", want: coverageTime, wantPresent: true},
		{name: "pointer to struct encoded as null", encoding: testEncoding{}, path: "ptrNil", want: nil, wantPresent: true},
		{name: "pointer to struct encoded as null mid path", encoding: testEncoding{}, path: "ptrNil.x", want: nil, wantPresent: false},
		{name: "plain value with encoding", encoding: testEncoding{}, path: "str", want: "a", wantPresent: true},
		{name: "typed nil encoding", encoding: (*nilEncoding)(nil), path: "str", want: "a", wantPresent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, present, err := evaluation.NewReader(tc.encoding, 0).Lookup(nested, tc.path)
			require.NoError(t, err)
			require.Equal(t, tc.wantPresent, present)
			require.Equal(t, tc.want, got)
		})
	}
}

type nilEncoding struct{ testEncoding }

func TestCoverageReaderBudgets(t *testing.T) {
	t.Parallel()

	data := map[string]any{
		"ptr":   new(new("p")),
		"items": []any{map[string]any{"name": "x"}, map[string]any{"name": "y"}},
	}

	for _, tc := range []struct {
		name    string
		reader  evaluation.FieldReader
		path    string
		wantErr bool
	}{
		{name: "pointer chain within budget", reader: evaluation.NewReader(nil, 4), path: "ptr"},
		{name: "pointer chain over budget", reader: evaluation.NewReader(nil, 3), path: "ptr", wantErr: true},
		{name: "slice over budget", reader: evaluation.NewReader(nil, 3), path: "items.name", wantErr: true},
		{name: "slice element over budget", reader: evaluation.NewReader(nil, 5), path: "items.name", wantErr: true},
		{name: "refusing parent budget", reader: evaluation.NewReader(nil, 100).BindBudget(refusingBudget{}), path: "ptr", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := tc.reader.Lookup(data, tc.path)
			if tc.wantErr {
				require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
				return
			}

			require.NoError(t, err)
		})
	}
}

// TestCoverageReaderMatchesStoredValueThroughPointers checks the reader reads a value behind a pointer in the stored form the
// evaluator reads under a profile (evaluator.StoredValue): a struct encoded from its address, a type of the encoding stored.
func TestCoverageReaderMatchesStoredValueThroughPointers(t *testing.T) {
	t.Parallel()

	data := map[string]any{"ptrPt": &point{X: 4}, "ptrWrap": &wrapped{V: "pw"}, "ptrTime": &coverageTime}
	reader := evaluation.NewReader(testEncoding{}, 0)

	for path, value := range data {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			got, present, err := reader.Lookup(data, path)
			require.NoError(t, err)
			require.True(t, present)
			require.Equal(t, evaluator.StoredValue(value, testEncoding{}, nil), got)
		})
	}
}

// reencodingEncoding handles every value it is given, as an encoding through an application registry does, and encodes a base
// form again by marking it, so that a value passed to it twice is told apart from one passed once.
type reencodingEncoding struct{ testEncoding }

type reencoded struct{ v any }

func (reencodingEncoding) Stored(v any, _ evaluator.Budget) (any, bool) {
	if p, ok := v.(point); ok {
		return map[string]any{"x": int64(p.X), "inner": map[string]any{"y": int64(p.X)}, "list": []any{int64(p.X)}}, true
	}

	return reencoded{v: v}, true
}

// TestReaderNeverStoresBaseForms: review finding R10-A1 — the reader passed every value, the base forms a stored document is made
// of included, to the encoding's Stored, so an encoding through an application registry encoded a document it had returned again
// at every later path step. A base form is in its stored form and is read as it is.
func TestReaderNeverStoresBaseForms(t *testing.T) {
	t.Parallel()

	reader := evaluation.NewReader(reencodingEncoding{}, 0)
	data := map[string]any{"p": point{X: 3}, "doc": map[string]any{"s": "a", "n": int64(1)}}

	for path, want := range map[string]any{
		"p.x":       int64(3),
		"p.inner.y": int64(3),
		"p.list":    []any{int64(3)},
		"doc.s":     "a",
		"doc.n":     int64(1),
	} {
		got, present, err := reader.Lookup(data, path)
		require.NoError(t, err, path)
		require.True(t, present, path)
		require.Equal(t, want, got, path)
	}
}
