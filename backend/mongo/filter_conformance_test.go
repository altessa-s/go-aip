// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/altessa-s/go-aip/backend/mongo/mongotest"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// TestFilterConformance renders the ordered query (TranslateD) as relaxed Extended JSON; Translate must fail alike. The
// expectations are shared with the live MongoDB suite of integration/docker.
func TestFilterConformance(t *testing.T) {
	t.Parallel()

	filtertest.RunConformance(t, mongotest.FilterConformanceBackend())
}
