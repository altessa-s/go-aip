// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package postgres_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

// TestSchemaPolicyKeepsDrift accepts only the deviations its policy names: another column of the same kind still fails.
func TestSchemaPolicyKeepsDrift(t *testing.T) {
	t.Parallel()

	db := assetStore(t)
	_, err := db.ExecContext(t.Context(), `ALTER TABLE assets ADD COLUMN level integer NOT NULL DEFAULT 0, ADD COLUMN note text,
		ADD COLUMN tag text COLLATE "C" NOT NULL DEFAULT ''`)
	require.NoError(t, err)

	for name, field := range map[string]listing.Field{
		"narrow integer": {Name: "level", Kind: filter.FieldKindInt},
		"collation":      {Name: "note", Kind: filter.FieldKindString, Presence: true},
		"nullability":    {Name: "tag", Kind: filter.FieldKindString, Presence: true},
		"uuid as text":   {Name: "part", Kind: filter.FieldKindInt},
	} {
		fields := []listing.Field{{Name: "id", Kind: filter.FieldKindString}, field}
		l, err := postgres.NewListEngine(db, pagetokentest.Codec(t), listing.MustSchema(fields, []string{"id"}, nil), "assets",
			[]string{"id"}, idDest, sqlbase.WithListIDType(sqlbase.IDUUID), sqlbase.WithListSchemaCheckPolicy(assetPolicy))
		require.NoError(t, err, name)
		require.ErrorIs(t, l.CheckSchema(t.Context(), db), sqlbase.ErrInvalidSchema, name)
	}
}
