// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing/evaluation"
)

// TestCoverageOperandShapes drives operands that only a policy without field rules lets through.
func TestCoverageOperandShapes(t *testing.T) {
	t.Parallel()

	schema := coverageSchema(t)
	profile := newCoverageProfile(t, schema)
	record := coverageRecord(nil)
	nullRating := coverageRecord(map[string]any{"rating": nil})

	runCases(t, profile, anyFieldPolicy(t), rawParser, []coverageCase{
		{name: "int vs float literal", expr: `count < 5.5`, data: record, want: true},
		{name: "string vs int ordering", expr: `name < 1`, data: record, want: false},
		{name: "int vs string ordering", expr: `count < "x"`, data: record, want: false},
		{name: "timestamp vs string ordering", expr: `at < "x"`, data: record, want: false},
		{name: "timestamp vs string equality", expr: `at == "x"`, data: record, want: false},
		{name: "bool ordering gt", expr: `flag > false`, data: record, want: true},
		{name: "bool ordering lt", expr: `flag < true`, data: coverageRecord(map[string]any{"flag": false}), want: true},
		{name: "bool ordering equal", expr: `flag >= true`, data: record, want: true},
		{name: "bool vs string ordering", expr: `flag < "x"`, data: record, want: false},
		{name: "bytes ordering", node: bin(filter.OpLT, ident("blob"), lit([]byte("z"))), data: record, want: true},
		{name: "bytes equality", node: bin(filter.OpEqual, ident("blob"), lit([]byte("xy"))), data: record, want: true},
		{name: "bytes vs string ordering", node: bin(filter.OpLT, ident("blob"), lit("z")), data: record, want: false},
		{name: "bytes vs string equality", node: bin(filter.OpEqual, ident("blob"), lit("xy")), data: record, want: false},
		{name: "missing bytes reads empty", node: bin(filter.OpEqual, ident("blob"), lit([]byte{})), data: without("blob"), want: true},
		{name: "presence bytes null", node: bin(filter.OpEqual, ident("pblob"), lit(nil)), data: record, want: true},
		{name: "absent optional bytes", node: bin(filter.OpEqual, ident("optBlob"), lit([]byte{})), data: record, want: true},
		{name: "opaque value ordering", expr: `name < "z"`, data: coverageRecord(map[string]any{"name": struct{}{}}), want: false},
		{name: "required eq null reads zero", expr: `name == null`, data: coverageRecord(map[string]any{"name": ""}), want: true},
		{name: "required ne null", expr: `count != null`, data: record, want: true},
		{name: "missing required eq null", expr: `name == null`, data: without("name"), want: false},
		{name: "decimal eq null reads zero", expr: `price == null`, data: coverageRecord(map[string]any{"price": decimal.Zero}), want: true},
		{name: "timestamp eq null reads zero", expr: `at == null`, data: coverageRecord(map[string]any{"at": time.Time{}}), want: true},
		{name: "float eq null reads zero", expr: `score == null`, data: coverageRecord(map[string]any{"score": 0.0}), want: true},
		{name: "bool eq null reads zero", expr: `flag == null`, data: coverageRecord(map[string]any{"flag": false}), want: true},
		{name: "bytes eq null reads zero", expr: `blob == null`, data: coverageRecord(map[string]any{"blob": []byte{}}), want: true},
		{name: "undeclared field eq null", expr: `other == null`, data: record, want: true},
		{name: "undeclared field eq value", expr: `other == 1`, data: coverageRecord(map[string]any{"other": 1}), want: true},
		{name: "undeclared field in null", expr: `other in [1, null]`, data: record, want: true},
		{name: "undeclared field has", node: call(filter.OpHas, ident("other")), data: record, want: false},
		{name: "optional in null element", expr: `note in ["x", null]`, data: record, want: true},
		{name: "enum in null element", expr: `status in ["active", null]`, data: record, want: true},
		{name: "presence null in null element", expr: `rating in [1, null]`, data: nullRating, want: true},
		{name: "presence null ordering with null", expr: `!(rating < null)`, data: nullRating, want: true},
		{name: "size of non string", expr: `size(count) == 0`, data: record, want: true},
		{name: "size compared with null", expr: `size(name) == null`, data: record, want: false},
		{name: "array ordering with null", expr: `nums < null`, data: record, want: false},
		{name: "array in list with null", expr: `tags in ["blue", null]`, data: record, want: false},
		{name: "non-string element in string function", expr: `tags.contains("a")`, data: coverageRecord(map[string]any{"tags": []any{int64(1), "a"}}), want: true},
		{name: "non-string elements only", expr: `tags.contains("a")`, data: coverageRecord(map[string]any{"tags": []any{int64(1)}}), want: false},
		{name: "large unsigned vs decimal", node: bin(filter.OpGT, ident("other"), lit(decimal.NewFromInt(1))), data: coverageRecord(map[string]any{"other": uint64(math.MaxUint64)}), want: true},
		{name: "decimal vs large unsigned", node: bin(filter.OpLT, ident("other"), lit(uint64(math.MaxUint64))), data: coverageRecord(map[string]any{"other": decimal.NewFromInt(1)}), want: true},
		{name: "decimal vs string", expr: `other == "x"`, data: coverageRecord(map[string]any{"other": decimal.NewFromInt(1)}), want: false},
		{name: "string vs decimal", node: bin(filter.OpLT, ident("other"), lit(decimal.NewFromInt(1))), data: coverageRecord(map[string]any{"other": "x"}), want: false},
		{name: "NaN is unordered", expr: `other < 1`, data: coverageRecord(map[string]any{"other": math.NaN()}), want: false},
		{name: "decimal in list", expr: `price in [1, 12.5]`, data: record, want: true},
		{name: "unknown or error-free path", expr: `rating > 1 || other == 1`, data: nullRating, want: false},
	})
}

// TestCoverageOperandErrors covers the operand shapes the profile refuses at evaluation time.
func TestCoverageOperandErrors(t *testing.T) {
	t.Parallel()

	schema := coverageSchema(t)
	profile := newCoverageProfile(t, schema)
	record := coverageRecord(nil)

	runCases(t, profile, anyFieldPolicy(t), rawParser, []coverageCase{
		{name: "invalid decimal literal", node: bin(filter.OpEqual, ident("price"), lit("abc")), data: record, anyErr: true},
		{name: "invalid stored decimal", expr: `price == 1`, data: coverageRecord(map[string]any{"price": "abc"}), anyErr: true},
		{name: "non-finite stored float", expr: `score == 1.0`, data: coverageRecord(map[string]any{"score": math.Inf(-1)}), anyErr: true},
		{name: "in list with invalid decimal", node: bin(filter.OpIn, ident("price"), &filter.ListNode{Elements: []filter.Node{lit("abc")}}), data: record, anyErr: true},
		{name: "invalid decimal in decimal array", node: bin(filter.OpIn, lit("abc"), ident("prices")), data: record, anyErr: true},
	})
}

var errLookup = errors.New("lookup failed")

type failingReader struct{ field string }

func (r failingReader) Lookup(data map[string]any, name string) (any, bool, error) {
	if name == r.field {
		return nil, false, errLookup
	}

	v, ok := data[name]

	return v, ok, nil
}

// TestCoverageReaderErrors checks a reader failure surfaces from every operator.
func TestCoverageReaderErrors(t *testing.T) {
	t.Parallel()

	schema := coverageSchema(t)
	record := coverageRecord(nil)

	profile, err := evaluation.New(schema, failingReader{field: "name"})
	if err != nil {
		t.Fatal(err)
	}

	runCases(t, profile, anyFieldPolicy(t), rawParser, []coverageCase{
		{name: "comparison", expr: `name == "a"`, data: record, wantErr: errLookup},
		{name: "size", expr: `size(name) == 1`, data: record, wantErr: errLookup},
		{name: "string function", expr: `name.contains("a")`, data: record, wantErr: errLookup},
		{name: "has", node: call(filter.OpHas, ident("name")), data: record, wantErr: errLookup},
		{name: "in list", expr: `name in ["a"]`, data: record, wantErr: errLookup},
		{name: "bare identifier", expr: `name`, data: record, wantErr: errLookup},
		{name: "and right", expr: `flag && name == "a"`, data: record, wantErr: errLookup},
		{name: "or right", expr: `!flag || name == "a"`, data: record, wantErr: errLookup},
		{name: "and left", expr: `name == "a" && flag`, data: record, wantErr: errLookup},
	})

	arrayProfile, err := evaluation.New(schema, failingReader{field: "tags"})
	if err != nil {
		t.Fatal(err)
	}

	runCases(t, arrayProfile, anyFieldPolicy(t), rawParser, []coverageCase{
		{name: "literal in array", expr: `"a" in tags`, data: record, wantErr: errLookup},
	})
}
