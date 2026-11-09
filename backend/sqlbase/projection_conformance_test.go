// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"strings"
	"testing"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/projection"
	"github.com/altessa-s/go-aip/projection/projectiontest"
)

func TestProjectionConformance(t *testing.T) {
	t.Parallel()

	for _, d := range projectionDialects() {
		if d.name == "clickhouse" {
			continue // backend/clickhouse runs the suite with its exclusion capability
		}

		t.Run(d.name, func(t *testing.T) {
			t.Parallel()

			projectiontest.RunConformance(t, projectiontest.Backend{
				New: func(p *projection.Policy, storage ...projection.TranslatorOption) (projectiontest.Translator, error) {
					tr, err := sqlbase.NewProjectionTranslator(p, d.dialect, sqlbase.WithProjectionOptions(storage...))
					if err != nil {
						return nil, err
					}

					return projectiontest.Adapt(tr, d.decode), nil
				},
			})
		})
	}
}

// decode reads a rendered column list back into storage names.
func (d projectionDialectCase) decode(cols string) (projectiontest.Result, error) {
	if cols == "" {
		return projectiontest.Result{All: true}, nil
	}

	return projectiontest.Result{Include: d.names(cols)}, nil
}

func (d projectionDialectCase) names(cols string) []string {
	out := strings.Split(cols, ", ")
	for i, c := range out {
		out[i] = strings.ReplaceAll(c, d.quote, "")
	}

	return out
}
