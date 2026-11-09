// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/altessa-s/go-aip/backend/mongo"
)

func BenchmarkCheckSizeOperand(b *testing.B) {
	for b.Loop() {
		_ = mongo.CheckSizeOperand("tags", int64(3))
	}
}
