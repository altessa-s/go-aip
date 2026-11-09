// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

func BenchmarkOrderTranslate_Simple(b *testing.B) {
	ob := orderbytest.MustParseOrderBy(b, "create_time desc")
	trans := mustOrderTranslator(b, orderbytest.MustPolicy(b))

	for b.Loop() {
		_, _ = trans.Translate(ob)
	}
}

func BenchmarkOrderTranslate_Multi(b *testing.B) {
	ob := orderbytest.MustParseOrderBy(b,
		"create_time desc, organization_id asc, slug, address.city desc, updated_at")
	trans := mustOrderTranslator(b, orderbytest.MustPolicy(b))

	for b.Loop() {
		_, _ = trans.Translate(ob)
	}
}

// BenchmarkOrderTranslate_Wildcard locks in the O(P) overhead of the
// wildcard-prefix scan in IsFieldAllowed for a typical allow-list of
// mixed exact entries and wildcards.
func BenchmarkOrderTranslate_Wildcard(b *testing.B) {
	ob := orderbytest.MustParseOrderBy(b,
		"create_time desc, address.city asc, user.profile.name desc")
	trans := mustOrderTranslator(b, orderbytest.MustPolicy(b, orderby.WithAllowedFields(
		"create_time",
		"address.*",
		"user.*",
		"billing.*",
		"audit.*",
	)))

	for b.Loop() {
		_, _ = trans.Translate(ob)
	}
}

// BenchmarkOrderTranslate_PrefixMapping locks in the O(P) overhead of the
// fieldPrefixMapping linear scan in ApplyFieldMapping.
func BenchmarkOrderTranslate_PrefixMapping(b *testing.B) {
	ob := orderbytest.MustParseOrderBy(b,
		"address.city desc, user.profile.name asc, billing.account.id desc")
	trans := mustOrderTranslator(b, orderbytest.MustPolicy(b), orderby.WithFieldPrefixMapping(map[string]string{
		"address.":      "addr.",
		"user.profile.": "users.prof.",
		"user.":         "users.",
		"billing.":      "bill.",
		"audit.":        "log.",
	}))

	for b.Loop() {
		_, _ = trans.Translate(ob)
	}
}
