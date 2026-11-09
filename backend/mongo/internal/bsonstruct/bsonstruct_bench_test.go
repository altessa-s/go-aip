// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package bsonstruct_test

import (
	"reflect"
	"testing"

	"github.com/altessa-s/go-aip/backend/mongo/internal/bsonstruct"
)

func BenchmarkReadField(b *testing.B) {
	typ := reflect.TypeFor[fields]()

	for b.Loop() {
		for f := range typ.Fields() {
			_ = bsonstruct.ReadField(f)
		}
	}
}
