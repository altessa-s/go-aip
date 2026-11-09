// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func BenchmarkMaskSpecPlan(b *testing.B) {
	msg := &pb.Resource{Name: "name"}
	spec, err := update.NewSpec(msg)
	require.NoError(b, err)
	b.ReportAllocs()

	for b.Loop() {
		_, err = spec.Plan(msg, maskOf([]string{"name"}))
		if err != nil {
			b.Fatal(err)
		}
	}
}
