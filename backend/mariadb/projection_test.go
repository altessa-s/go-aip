// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/mariadb/mariadbtest"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/projection"
	"github.com/altessa-s/go-aip/projection/projectiontest"
)

func TestProjectionConformance(t *testing.T) {
	t.Parallel()

	projectiontest.RunConformance(t, projectiontest.Backend{
		New: func(p *projection.Policy, storage ...projection.TranslatorOption) (projectiontest.Translator, error) {
			tr, err := mariadb.NewProjectionTranslator(p, sqlbase.WithProjectionOptions(storage...))
			if err != nil {
				return nil, err
			}

			return projectiontest.Adapt(tr, mariadbtest.DecodeColumns), nil
		},
	})
}

func TestNewProjectionTranslator(t *testing.T) {
	t.Parallel()

	tr, err := mariadb.NewProjectionTranslator(projectiontest.MustPolicy(t, projection.WithAllowedFields("name", "createTime")),
		sqlbase.WithProjectionOptions(projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
			projection.WithRequiredFields("id")))
	require.NoError(t, err)

	got, err := tr.Translate(projection.Spec{Paths: []string{"createTime"}})
	require.NoError(t, err)
	require.Equal(t, "`created_at`, `id`", got)

	_, err = mariadb.NewProjectionTranslator(nil)
	require.ErrorIs(t, err, projection.ErrPolicyRequired)

	_, err = mariadb.NewProjectionTranslator(projectiontest.MustPolicy(t, projection.WithDeniedFields("password")))
	require.ErrorIs(t, err, projection.ErrAllFieldsUnresolvable)
}
