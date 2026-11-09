// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/projection"
)

func mustPolicy(tb testing.TB, opts ...projection.PolicyOption) *projection.Policy {
	tb.Helper()

	p, err := projection.NewPolicy(opts...)
	require.NoError(tb, err)

	return p
}

// TestProjectionPolicySharedAcrossTranslators binds one policy to two backends with
// different storage names: both accept and refuse the same requests, and
// a denied API field stays denied through each backend's own alias.
func TestProjectionPolicySharedAcrossTranslators(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t,
		projection.WithUntrustedInput(true),
		projection.WithAllowedFields("name", "createTime", "alias", "hash"),
		projection.WithDeniedFields("hash"),
		projection.WithView(projection.ViewBasic, "name", "createTime"),
	)

	pg, err := sqlbase.NewProjectionTranslator(p, postgres.ProjectionDialect(), sqlbase.WithProjectionOptions(projection.WithFieldMapping(map[string]string{"createTime": "created_at"})))
	require.NoError(t, err)
	doc, err := projection.NewTranslatorContext(p, projection.WithFieldMapping(map[string]string{"createTime": "meta.created"}))
	require.NoError(t, err)

	for _, tc := range []struct {
		spec projection.Spec
		sql  string
		doc  []string
		err  error
	}{
		{spec: projection.Spec{}, sql: `"alias", "created_at", "name"`, doc: []string{"alias", "meta.created", "name"}},
		{spec: projection.Spec{Paths: []string{"name"}}, sql: `"name"`, doc: []string{"name"}},
		{spec: projection.Spec{Paths: []string{"hash"}}, err: projection.ErrFieldNotAllowed},
		{spec: projection.Spec{Paths: []string{"secret"}}, err: projection.ErrFieldNotAllowed},
	} {
		_, policyErr := p.Validate(tc.spec)
		require.ErrorIs(t, policyErr, tc.err)

		gotSQL, err := pg.Translate(tc.spec)
		require.ErrorIs(t, err, tc.err)
		require.Equal(t, tc.sql, gotSQL)

		sel, err := doc.Resolve(tc.spec)
		require.ErrorIs(t, err, tc.err)
		require.Equal(t, tc.doc, sel.Include)
	}

	// Each backend maps its own alias onto the denied field's storage name.
	aliased, err := projection.NewTranslatorContext(p, projection.WithFieldMapping(map[string]string{"alias": "pw", "hash": "pw"}))
	require.NoError(t, err)
	_, err = aliased.Resolve(projection.Spec{Paths: []string{"alias"}})
	require.ErrorIs(t, err, projection.ErrFieldNotAllowed)
	require.Same(t, p, aliased.Policy())
}
