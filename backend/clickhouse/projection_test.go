// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/clickhouse/clickhousetest"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/projection"
	"github.com/altessa-s/go-aip/projection/projectiontest"
)

func TestProjectionConformance(t *testing.T) {
	t.Parallel()

	projectiontest.RunConformance(t, projectiontest.Backend{
		New: func(p *projection.Policy, storage ...projection.TranslatorOption) (projectiontest.Translator, error) {
			tr, err := clickhouse.NewProjectionTranslator(p, sqlbase.WithProjectionOptions(storage...))
			if err != nil {
				return nil, err
			}

			return projectiontest.Adapt(tr, clickhousetest.DecodeColumns), nil
		},
		Capabilities: projectiontest.Capabilities{Exclusion: true},
	})
}

func TestNewProjectionTranslator_NilPolicy(t *testing.T) {
	t.Parallel()

	_, err := clickhouse.NewProjectionTranslator(nil)
	require.ErrorIs(t, err, projection.ErrPolicyRequired)
}
