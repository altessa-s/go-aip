// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili_test

import (
	"testing"

	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

func BenchmarkOrderTranslate_Simple(b *testing.B) {
	ob := orderbytest.MustParseOrderBy(b, "createdAt desc")
	trans := mustTranslator(b, orderbytest.MustPolicy(b))

	for b.Loop() {
		_, _ = trans.Translate(ob)
	}
}

func BenchmarkOrderTranslate_Multi(b *testing.B) {
	ob := orderbytest.MustParseOrderBy(b,
		"createdAt desc, organization_id asc, slug, address.city desc, updated_at")
	trans := mustTranslator(b, orderbytest.MustPolicy(b))

	for b.Loop() {
		_, _ = trans.Translate(ob)
	}
}
