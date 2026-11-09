// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation_test

import (
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/filter"
)

func TestCoverageArrayFields(t *testing.T) {
	t.Parallel()

	schema := coverageSchema(t)
	profile := newCoverageProfile(t, schema)
	record := coverageRecord(nil)
	empty := coverageRecord(map[string]any{"tags": []string{}, "nums": nil})
	missing := without("tags", "nums", "prices", "times", "floats")

	runCases(t, profile, schema.FilterPolicy(), schemaParser(schema), []coverageCase{
		{name: "membership eq", expr: `tags == "red"`, data: record, want: true},
		{name: "membership eq miss", expr: `tags == "blue"`, data: record, want: false},
		{name: "membership ne", expr: `tags != "blue"`, data: record, want: true},
		{name: "membership ne hit", expr: `tags != "red"`, data: record, want: false},
		{name: "element ordering", expr: `nums > 2`, data: record, want: true},
		{name: "element ordering miss", expr: `nums > 3`, data: record, want: false},
		{name: "literal in array", expr: `"green" in tags`, data: record, want: true},
		{name: "literal not in array", expr: `"blue" in tags`, data: record, want: false},
		{name: "int literal in array", expr: `2 in nums`, data: record, want: true},
		{name: "array in list", expr: `tags in ["blue", "green"]`, data: record, want: true},
		{name: "array in list miss", expr: `tags in ["blue", "white"]`, data: record, want: false},
		{name: "array eq null when empty", expr: `tags == null`, data: empty, want: true},
		{name: "array eq null when set", expr: `tags == null`, data: record, want: false},
		{name: "array ne null when set", expr: `tags != null`, data: record, want: true},
		{name: "array ne null when empty", expr: `tags != null`, data: empty, want: false},
		{name: "array eq null when missing", expr: `nums == null`, data: missing, want: true},
		{name: "membership on missing", expr: `tags == "red"`, data: missing, want: false},
		{name: "size of array", expr: `size(tags) == 2`, data: record, want: true},
		{name: "size of missing array", expr: `size(tags) == 0`, data: missing, want: true},
		{name: "size of string counts runes", expr: `size(name) == 3`, data: coverageRecord(map[string]any{"name": "héé"}), want: true},
		{name: "size ordering", expr: `size(nums) > 2`, data: record, want: true},
		{name: "string fn any element", expr: `tags.startsWith("gr")`, data: record, want: true},
		{name: "string fn no element", expr: `tags.endsWith("x")`, data: record, want: false},
		{name: "string fn on empty", expr: `tags.contains("r")`, data: empty, want: false},
		{name: "matches element", expr: `tags.matches("^re")`, data: record, want: true},
		{name: "has array set", node: call(filter.OpHas, ident("tags")), data: record, want: true},
		{name: "has array empty", node: call(filter.OpExists, ident("tags")), data: empty, want: false},
		{name: "decimal array eq", expr: `prices == 2.2`, data: record, want: true},
		{name: "decimal array ordering", expr: `prices > 2.1`, data: record, want: true},
		{name: "decimal literal in array", expr: `1.1 in prices`, data: record, want: true},
		{name: "decimal array in list", expr: `prices in [3.3, 1.1]`, data: record, want: true},
		{name: "decimal array missing", expr: `prices == 1.1`, data: missing, want: false},
		{name: "timestamp array truncated", expr: `times == timestamp("2026-03-01T13:00:00Z")`, data: coverageRecord(map[string]any{
			"times": []time.Time{coverageTime.Add(time.Hour + time.Nanosecond)},
		}), want: true},
		{name: "timestamp array ordering", expr: `times > timestamp("2026-03-01T12:30:00Z")`, data: record, want: true},
		{name: "float array", expr: `floats < 1`, data: record, want: true},
		{name: "pointer elements", expr: `tags == "red"`, data: coverageRecord(map[string]any{"tags": []*string{new("red"), nil}}), want: true},
		{name: "fixed array", expr: `nums == 2`, data: coverageRecord(map[string]any{"nums": [2]int64{1, 2}}), want: true},
		{name: "scalar stored in array field", expr: `tags == "red"`, data: coverageRecord(map[string]any{"tags": "red"}), want: false},
		{name: "non-finite float element", expr: `floats > 0`, data: coverageRecord(map[string]any{"floats": []float64{1, math.NaN()}}), anyErr: true},
		{name: "infinite float element", expr: `floats > 0`, data: coverageRecord(map[string]any{"floats": []any{math.Inf(1)}}), anyErr: true},
		{name: "invalid decimal element", expr: `prices == 1.1`, data: coverageRecord(map[string]any{"prices": []any{"not-a-number"}}), anyErr: true},
		{name: "decimal elements typed", expr: `prices == 1.1`, data: coverageRecord(map[string]any{"prices": []decimal.Decimal{decimal.RequireFromString("1.1")}}), want: true},
	})
}
