// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter/filtertest"
)

func BenchmarkContractTranslate(b *testing.B) {
	tr := contractTranslator(b)
	node := filtertest.MustParseFilter(b, `name in ["a", "b"] && tags.startsWith("x") && price > 10000000000000000.01`)
	b.ReportAllocs()

	for b.Loop() {
		_, _, err := tr.Translate(node)
		require.NoError(b, err)
	}
}
