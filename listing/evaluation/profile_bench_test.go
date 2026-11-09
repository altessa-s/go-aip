// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/evaluation"
)

func BenchmarkReaderLookup(b *testing.B) {
	r := evaluation.NewReader(nil, 1024)

	data := map[string]any{"nested": map[string]string{"id": "a"}}
	for b.Loop() {
		_, _, _ = r.Lookup(data, "nested.id")
	}
}

func BenchmarkReaderSharedBudget(b *testing.B) {
	budget := &countingBudget{}
	r := evaluation.NewReader(nil, 1024).BindBudget(budget)

	data := map[string]any{"nested": map[string]string{"id": "a"}}
	for b.Loop() {
		_, _, _ = r.Lookup(data, "nested.id")
	}
}

// BenchmarkProgramEvaluate evaluates one compiled filter over records of every common shape: an enum list, integer and float
// comparisons, a decimal, a timestamp, a string function and an array membership.
func BenchmarkProgramEvaluate(b *testing.B) {
	schema, err := listing.NewSchema(benchFields(), []string{"id"}, listing.NewProfile())
	if err != nil {
		b.Fatal(err)
	}

	query, err := schema.Compile(b.Context(), listing.Request{
		Filter: `status in ["ACTIVE", "INACTIVE"] && count > 1 && score <= 9.5 && price >= 1.25 && ` +
			`at > timestamp("2026-01-01T00:00:00Z") && name.startsWith("n") && "x" in tags`,
	})
	if err != nil {
		b.Fatal(err)
	}

	profile, err := evaluation.New(schema, evaluation.NewReader(nil, 0))
	if err != nil {
		b.Fatal(err)
	}

	program, err := profile.CompileEvaluator(schema.FilterPolicy(), query.Filter)
	if err != nil {
		b.Fatal(err)
	}

	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	records := make([]map[string]any, 64)

	for i := range records {
		records[i] = map[string]any{
			"id": fmt.Sprintf("r-%02d", i), "name": fmt.Sprintf("n-%d", i), "status": []string{"active", "inactive", ""}[i%3],
			"count": int64(i), "score": float64(i) / 4, "price": decimal.New(int64(i), -1), "at": at.Add(time.Duration(i) * time.Hour),
			"tags": []string{"a", "x"}[i%2:],
		}
	}

	i := 0
	for b.Loop() {
		if _, err := program.Evaluate(records[i%len(records)], evaluator.DefaultMaxDataBudget, nil); err != nil {
			b.Fatal(err)
		}

		i++
	}
}

func benchFields() []listing.Field {
	return []listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "name", Kind: filter.FieldKindString},
		{
			Name: "status", Kind: filter.FieldKindString, Optional: true, EnumZero: "STATUS_UNSPECIFIED",
			Enum: map[string]any{"STATUS_UNSPECIFIED": "", "ACTIVE": "active", "INACTIVE": "inactive"},
		},
		{Name: "count", Kind: filter.FieldKindInt},
		{Name: "score", Kind: filter.FieldKindFloat},
		{Name: "price", Kind: filter.FieldKindDecimal},
		{Name: "at", Kind: filter.FieldKindTimestamp},
		{Name: "tags", Kind: filter.FieldKindString, Array: true},
	}
}
