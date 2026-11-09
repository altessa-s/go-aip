// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgrestest

import (
	"strings"

	"github.com/altessa-s/go-aip/projection/projectiontest"
)

// DecodeColumns reads a column list rendered by the PostgreSQL projection translator back into storage names, for
// [projectiontest.Adapt]: "" selects every column.
func DecodeColumns(cols string) (projectiontest.Result, error) {
	if cols == "" {
		return projectiontest.Result{All: true}, nil
	}

	out := strings.Split(cols, ", ")
	for i, c := range out {
		out[i] = strings.ReplaceAll(c, `"`, "")
	}

	return projectiontest.Result{Include: out}, nil
}
