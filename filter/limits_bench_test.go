// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func BenchmarkCheckLimits(b *testing.B) {
	ctx, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(b, err)
	node := filtertest.MustParseFilter(b, `active && age >= 18 && status in ["active", "pending"]`)
	b.ReportAllocs()

	for b.Loop() {
		if err := ctx.CheckLimits(node); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCheckFields(b *testing.B) {
	policy, err := filter.NewPolicy(filter.WithAllowedFields("active", "age", "status"))
	require.NoError(b, err)
	node := filtertest.MustParseFilter(b, `active && age >= 18 && status in ["active", "pending"]`)
	b.ReportAllocs()

	for b.Loop() {
		_ = policy.CheckFields(node)
	}
}

func BenchmarkPolicyField(b *testing.B) {
	policy, err := filter.NewPolicy(filter.WithAllowedFields("age"))
	require.NoError(b, err)
	b.ReportAllocs()

	for b.Loop() {
		_ = policy.Field("age").Allowed()
	}
}
