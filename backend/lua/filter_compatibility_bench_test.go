// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package lua_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/lua"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func BenchmarkFilterTranslatorValidateAST(b *testing.B) {
	tr, err := lua.NewFilterTranslator(filtertest.MustPolicy(b, filter.WithAnyField(true)))
	require.NoError(b, err)

	for b.Loop() {
		_ = tr.ValidateAST(nil)
	}
}
