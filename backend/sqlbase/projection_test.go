// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/projection"
)

func TestNewContext(t *testing.T) {
	t.Parallel()

	_, err := newContext(postgres.ProjectionDialect(), projection.WithDeniedFields("password"))
	require.ErrorIs(t, err, projection.ErrAllFieldsUnresolvable, "SQL cannot express an exclusion")

	_, err = newContext(postgres.ProjectionDialect(), projection.WithDeniedFields("password"), projection.WithAllowedFields("name"))
	require.NoError(t, err)

	_, err = newContext(postgres.ProjectionDialect(),
		projection.WithView(projection.ViewBasic, "raw"),
		projection.WithFieldMapping(map[string]string{"raw": "lower(name)"}),
	)
	require.ErrorIs(t, err, projection.ErrInvalidFieldPath, "a view that is not a column fails at construction")

	_, err = newContext(postgres.ProjectionDialect(), projection.WithUntrustedInput(true))
	require.ErrorIs(t, err, projection.ErrAllowlistRequired)

	_, err = newContext(postgres.ProjectionDialect(),
		projection.WithAllowedFields("raw"),
		projection.WithFieldMapping(map[string]string{"raw": "lower(name)"}),
	)
	require.ErrorIs(t, err, projection.ErrInvalidFieldPath, "an allowed field that is not a column fails at construction")

	ctx, err := newContext(mariadb.ProjectionDialect(), projection.WithAllowedFields("name", "author.*"))
	require.NoError(t, err)
	got, err := sqlbase.TranslateProjection(ctx, mariadb.ProjectionDialect(), projection.Spec{})
	require.NoError(t, err)
	require.Equal(t, "`author`, `name`", got, "an omitted mask selects the allow-list roots")
}

func TestTranslateProjection(t *testing.T) {
	t.Parallel()

	ctx, err := newContext(postgres.ProjectionDialect(),
		projection.WithAllowedFields("*"),
		projection.WithFieldMapping(map[string]string{
			"createTime":  "created_at",
			"author.name": "authors.name",
			"author.bio":  "a.b.c",
			"raw":         "lower(name)",
		}),
		projection.WithRequiredFields("id"),
	)
	require.NoError(t, err)

	tests := []struct {
		name    string
		paths   []string
		style   sqlbase.ProjectionDialect
		want    string
		wantErr error
	}{
		{name: "every column", style: postgres.ProjectionDialect(), want: ""},
		{name: "postgres quoting", paths: []string{"name", "createTime"}, style: postgres.ProjectionDialect(), want: `"created_at", "id", "name"`},
		{name: "mysql quoting", paths: []string{"name"}, style: mariadb.ProjectionDialect(), want: "`id`, `name`"},
		{name: "table qualified", paths: []string{"author.name"}, style: postgres.ProjectionDialect(), want: `"authors"."name", "id"`},
		{name: "too deep for SQL", paths: []string{"author.bio"}, style: postgres.ProjectionDialect(), wantErr: projection.ErrInvalidFieldPath},
		{name: "expression rejected", paths: []string{"raw"}, style: postgres.ProjectionDialect(), wantErr: projection.ErrInvalidFieldPath},
		{name: "index", paths: []string{"author.0"}, style: postgres.ProjectionDialect(), wantErr: projection.ErrUnsupportedPath},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := sqlbase.TranslateProjection(ctx, tc.style, projection.Spec{Paths: tc.paths})
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestTranslateAllColumns(t *testing.T) {
	t.Parallel()

	ctx, err := newContext(postgres.ProjectionDialect())
	require.NoError(t, err)
	got, err := sqlbase.TranslateProjection(ctx, postgres.ProjectionDialect(), projection.Spec{})
	require.NoError(t, err)
	require.Empty(t, got, "every column renders empty so the caller writes *")

	got, err = sqlbase.TranslateProjectionView(ctx, postgres.ProjectionDialect(), projection.Spec{}, projection.ViewUnspecified)
	require.NoError(t, err)
	require.Empty(t, got)

	_, err = sqlbase.TranslateProjectionView(nil, postgres.ProjectionDialect(), projection.Spec{}, projection.ViewFull)
	require.ErrorIs(t, err, projection.ErrInvalidFieldPath)

	_, err = sqlbase.TranslateProjectionView(ctx, postgres.ProjectionDialect(), projection.Spec{Mode: projection.ModeNone}, projection.ViewFull)
	require.ErrorIs(t, err, projection.ErrUnsupportedSelection)
}
