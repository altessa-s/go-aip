// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse_test

import (
	"testing"

	"github.com/altessa-s/go-aip/backend/clickhouse/clickhousetest"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestConformance(t *testing.T) {
	t.Parallel()

	filtertest.RunConformance(t, clickhousetest.FilterConformanceBackend())
}
