// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation_test

import (
	"maps"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/filter"
)

var coverageTime = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// coverageRecord is a fully populated stored item; with overrides it replaces or, with a nil value, keeps a key as null.
func coverageRecord(overrides map[string]any) map[string]any {
	record := map[string]any{
		"id":     "a",
		"name":   "alpha",
		"flag":   true,
		"count":  int32(5),
		"score":  1.5,
		"price":  decimal.RequireFromString("12.50"),
		"at":     coverageTime,
		"blob":   []byte("xy"),
		"tags":   []string{"red", "green"},
		"nums":   []int{1, 2, 3},
		"floats": []float64{0.5, 2.5},
		"prices": []any{decimal.RequireFromString("1.10"), "2.20", nil},
		"times":  []time.Time{coverageTime, coverageTime.Add(time.Hour)},
		"rating": int64(4),
		"label":  "gold",
	}
	maps.Copy(record, overrides)

	return record
}

func without(keys ...string) map[string]any {
	record := coverageRecord(nil)
	for _, key := range keys {
		delete(record, key)
	}

	return record
}

func TestCoverageScalarComparisons(t *testing.T) {
	t.Parallel()

	schema := coverageSchema(t)
	profile := newCoverageProfile(t, schema)
	record := coverageRecord(nil)

	runCases(t, profile, schema.FilterPolicy(), schemaParser(schema), []coverageCase{
		{name: "string eq", expr: `name == "alpha"`, data: record, want: true},
		{name: "string ne", expr: `name != "alpha"`, data: record, want: false},
		{name: "string lt", expr: `name < "beta"`, data: record, want: true},
		{name: "string lte", expr: `name <= "alpha"`, data: record, want: true},
		{name: "string gt", expr: `name > "beta"`, data: record, want: false},
		{name: "string gte", expr: `name >= "alpha"`, data: record, want: true},
		{name: "int widened", expr: `count == 5`, data: record, want: true},
		{name: "int lt", expr: `count < 6`, data: record, want: true},
		{name: "int gt", expr: `count > 5`, data: record, want: false},
		{name: "uint stored", expr: `count == 5`, data: coverageRecord(map[string]any{"count": uint16(5)}), want: true},
		{name: "float", expr: `score >= 1.5`, data: record, want: true},
		{name: "float vs int", expr: `score > 1`, data: record, want: true},
		{name: "float32 stored", expr: `score == 1.5`, data: coverageRecord(map[string]any{"score": float32(1.5)}), want: true},
		{name: "bool eq", expr: `flag == true`, data: record, want: true},
		{name: "bool ne", expr: `flag != false`, data: record, want: true},
		{name: "bare bool true", expr: `flag`, data: record, want: true},
		{name: "bare bool false", expr: `flag`, data: coverageRecord(map[string]any{"flag": false}), want: false},
		{name: "not bare bool", expr: `!flag`, data: record, want: false},
		{name: "timestamp eq", expr: `at == timestamp("2026-03-01T12:00:00Z")`, data: record, want: true},
		{name: "timestamp lt", expr: `at < timestamp("2026-03-01T13:00:00Z")`, data: record, want: true},
		{name: "timestamp zone", expr: `at == timestamp("2026-03-01T12:00:00Z")`, data: coverageRecord(map[string]any{"at": coverageTime.In(time.FixedZone("x", 3600))}), want: true},
		{name: "decimal eq", expr: `price == 12.5`, data: record, want: true},
		{name: "decimal gt", expr: `price > 12.49`, data: record, want: true},
		{name: "decimal lt int", expr: `price < 13`, data: record, want: true},
		{name: "decimal string stored", expr: `price == 12.5`, data: coverageRecord(map[string]any{"price": "12.5"}), want: true},
		{name: "decimal ne", expr: `price != 12.5`, data: record, want: false},
		{name: "pointer stored", expr: `name == "alpha"`, data: coverageRecord(map[string]any{"name": new("alpha")}), want: true},
		{name: "nested pointer stored", expr: `count == 5`, data: coverageRecord(map[string]any{"count": new(new(int64(5)))}), want: true},
		// A required field absent from storage matches no value, as in a store that skips a missing field.
		{name: "missing required string", expr: `name == ""`, data: without("name"), want: false},
		{name: "missing required int lt", expr: `count < 1`, data: without("count"), want: false},
		{name: "missing required ne", expr: `name != "alpha"`, data: without("name"), want: true},
	})
}

func TestCoverageLogicalKleene(t *testing.T) {
	t.Parallel()

	schema := coverageSchema(t)
	profile := newCoverageProfile(t, schema)
	nullRating := coverageRecord(map[string]any{"rating": nil})

	// rating > 1 is unknown for a null rating; flag is true and name == "x" is false.
	runCases(t, profile, schema.FilterPolicy(), schemaParser(schema), []coverageCase{
		{name: "unknown alone", expr: `rating > 1`, data: nullRating, want: false},
		{name: "not unknown", expr: `!(rating > 1)`, data: nullRating, want: false},
		{name: "true and true", expr: `flag && name == "alpha"`, data: nullRating, want: true},
		{name: "true and false", expr: `flag && name == "x"`, data: nullRating, want: false},
		{name: "false and unknown", expr: `name == "x" && rating > 1`, data: nullRating, want: false},
		{name: "true and unknown", expr: `flag && rating > 1`, data: nullRating, want: false},
		{name: "not true and unknown", expr: `!(flag && rating > 1)`, data: nullRating, want: false},
		{name: "unknown and false", expr: `rating > 1 && name == "x"`, data: nullRating, want: false},
		{name: "not unknown and false", expr: `!(rating > 1 && name == "x")`, data: nullRating, want: true},
		{name: "unknown and true", expr: `rating > 1 && flag`, data: nullRating, want: false},
		{name: "not unknown and true", expr: `!(rating > 1 && flag)`, data: nullRating, want: false},
		{name: "unknown and unknown", expr: `!(rating > 1 && rating < 9)`, data: nullRating, want: false},
		{name: "true or unknown", expr: `flag || rating > 1`, data: nullRating, want: true},
		{name: "false or unknown", expr: `!(name == "x" || rating > 1)`, data: nullRating, want: false},
		{name: "false or false", expr: `name == "x" || name == "y"`, data: nullRating, want: false},
		{name: "unknown or true", expr: `rating > 1 || flag`, data: nullRating, want: true},
		{name: "unknown or false", expr: `!(rating > 1 || name == "x")`, data: nullRating, want: false},
		{name: "unknown or unknown", expr: `!(rating > 1 || rating < 9)`, data: nullRating, want: false},
		{name: "known or", expr: `rating > 1 || name == "x"`, data: coverageRecord(nil), want: true},
		{name: "presence bool null bare", expr: `!enabled`, data: coverageRecord(nil), want: false},
		{name: "presence bool true bare", expr: `enabled`, data: coverageRecord(map[string]any{"enabled": true}), want: true},
	})
}

func TestCoveragePresenceFields(t *testing.T) {
	t.Parallel()

	schema := coverageSchema(t)
	profile := newCoverageProfile(t, schema)
	record := coverageRecord(nil)
	null := coverageRecord(map[string]any{"rating": nil, "label": nil, "cost": nil})
	absent := without("rating", "label")

	runCases(t, profile, schema.FilterPolicy(), schemaParser(schema), []coverageCase{
		{name: "eq null when null", expr: `rating == null`, data: null, want: true},
		{name: "eq null when absent", expr: `rating == null`, data: absent, want: true},
		{name: "eq null when set", expr: `rating == null`, data: record, want: false},
		{name: "ne null when set", expr: `rating != null`, data: record, want: true},
		{name: "ne null when null", expr: `rating != null`, data: null, want: false},
		{name: "null pointer is null", expr: `label == null`, data: coverageRecord(map[string]any{"label": (*string)(nil)}), want: true},
		{name: "eq value when null", expr: `rating == 4`, data: null, want: false},
		{name: "ne value when null", expr: `rating != 4`, data: null, want: true},
		{name: "lt value when null", expr: `!(rating < 4)`, data: null, want: false},
		{name: "eq value when set", expr: `rating == 4`, data: record, want: true},
		{name: "zero is not null", expr: `rating == 0`, data: coverageRecord(map[string]any{"rating": 0}), want: true},
		{name: "has when set", node: call(filter.OpHas, ident("rating")), data: record, want: true},
		{name: "has when zero", node: call(filter.OpHas, ident("rating")), data: coverageRecord(map[string]any{"rating": 0}), want: true},
		{name: "has when null", node: call(filter.OpHas, ident("rating")), data: null, want: false},
		{name: "string fn on null", expr: `label.contains("o")`, data: null, want: false},
		{name: "not string fn on null", expr: `!label.contains("o")`, data: null, want: true},
		{name: "string fn on set", expr: `label.startsWith("go")`, data: record, want: true},
		{name: "in when null without null element", expr: `rating in [1, 2]`, data: null, want: false},
		{name: "in when set", expr: `rating in [3, 4]`, data: record, want: true},
		{name: "decimal presence null", expr: `cost == null`, data: null, want: true},
		{name: "decimal presence set", expr: `cost > 1`, data: coverageRecord(map[string]any{"cost": "2.5"}), want: true},
		{name: "decimal presence in", expr: `cost in [2.5, 3]`, data: coverageRecord(map[string]any{"cost": "2.5"}), want: true},
		{name: "size of null presence", expr: `size(label) == 0`, data: null, want: true},
	})
}

func TestCoverageOptionalAndEnumFields(t *testing.T) {
	t.Parallel()

	schema := coverageSchema(t)
	profile := newCoverageProfile(t, schema)
	record := coverageRecord(nil)

	runCases(t, profile, schema.FilterPolicy(), schemaParser(schema), []coverageCase{
		{name: "absent optional string is empty", expr: `note == ""`, data: record, want: true},
		{name: "nil optional string is empty", expr: `note == ""`, data: coverageRecord(map[string]any{"note": nil}), want: true},
		{name: "optional eq null", expr: `note == null`, data: record, want: true},
		{name: "optional set ne null", expr: `note != null`, data: coverageRecord(map[string]any{"note": "x"}), want: true},
		{name: "absent optional int", expr: `opt == 0`, data: record, want: true},
		{name: "absent optional float", expr: `optFloat == 0.0`, data: record, want: true},
		{name: "absent optional bool", expr: `optBool == false`, data: record, want: true},
		{name: "absent optional timestamp", expr: `optAt < timestamp("1970-01-01T00:00:00Z")`, data: record, want: true},
		{name: "absent optional decimal", expr: `optPrice == 0`, data: record, want: true},
		{name: "absent optional decimal null", expr: `optPrice == null`, data: record, want: true},
		{name: "optional in without match", expr: `note in ["x", "y"]`, data: record, want: false},
		{name: "has optional absent", node: call(filter.OpHas, ident("note")), data: record, want: false},
		{name: "has optional set", node: call(filter.OpHas, ident("note")), data: coverageRecord(map[string]any{"note": "x"}), want: true},
		{name: "has required zero", node: call(filter.OpHas, ident("count")), data: coverageRecord(map[string]any{"count": 0}), want: false},
		{name: "has required set", node: call(filter.OpHas, ident("count")), data: record, want: true},
		{name: "has bytes empty", node: call(filter.OpHas, ident("blob")), data: coverageRecord(map[string]any{"blob": []byte{}}), want: false},
		{name: "has bytes nil", node: call(filter.OpHas, ident("blob")), data: coverageRecord(map[string]any{"blob": []byte(nil)}), want: false},
		{name: "has bytes set", node: call(filter.OpHas, ident("blob")), data: record, want: true},
		{name: "has optional bytes absent", node: call(filter.OpHas, ident("optBlob")), data: record, want: false},
		{name: "enum eq", expr: `status == "ACTIVE"`, data: coverageRecord(map[string]any{"status": "active"}), want: true},
		{name: "enum absent is zero", expr: `status == "STATUS_UNSPECIFIED"`, data: record, want: true},
		{name: "enum absent eq null", expr: `status == null`, data: record, want: true},
		{name: "enum in", expr: `status in ["ACTIVE", "INACTIVE"]`, data: coverageRecord(map[string]any{"status": "inactive"}), want: true},
		{name: "has enum zero", node: call(filter.OpHas, ident("status")), data: record, want: false},
		{name: "has enum set", node: call(filter.OpHas, ident("status")), data: coverageRecord(map[string]any{"status": "active"}), want: true},
	})
}
