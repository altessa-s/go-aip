// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/postgres/postgrestest"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/projection"
	"github.com/altessa-s/go-aip/projection/projectiontest"
)

func TestProjectionConformance(t *testing.T) {
	t.Parallel()

	projectiontest.RunConformance(t, projectiontest.Backend{
		New: func(p *projection.Policy, storage ...projection.TranslatorOption) (projectiontest.Translator, error) {
			tr, err := postgres.NewProjectionTranslator(p, sqlbase.WithProjectionOptions(storage...))
			if err != nil {
				return nil, err
			}

			return projectiontest.Adapt(tr, postgrestest.DecodeColumns), nil
		},
	})
}

func TestNewProjectionTranslator_NilPolicy(t *testing.T) {
	t.Parallel()

	_, err := postgres.NewProjectionTranslator(nil)
	require.ErrorIs(t, err, projection.ErrPolicyRequired)
}

func TestNewProjectionTranslator_NoExclusion(t *testing.T) {
	t.Parallel()

	policy, err := projection.NewPolicy(projection.WithDeniedFields("password"))
	require.NoError(t, err)
	_, err = postgres.NewProjectionTranslator(policy)
	require.ErrorIs(t, err, projection.ErrAllFieldsUnresolvable)
}
