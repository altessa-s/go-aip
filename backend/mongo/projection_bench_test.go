// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/altessa-s/go-aip/projection"
)

func BenchmarkProjectionTranslate(b *testing.B) {
	tr := newProjectionTranslator(b,
		projection.WithAllowedFields("name", "email", "address.*", "createTime"),
		projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
		projection.WithRequiredFields("_id", "version"),
	)

	spec := projection.Spec{Paths: []string{"address", "address.city", "createTime", "email", "name"}}
	for b.Loop() {
		_, _ = tr.Translate(spec)
	}
}

func BenchmarkProjectionTranslateAllFields(b *testing.B) {
	tr := newProjectionTranslator(b, projection.WithDeniedFields("password"))
	for b.Loop() {
		_, _ = tr.Translate(projection.Spec{})
	}
}
