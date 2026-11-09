// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/projection"
)

func BenchmarkTranslateProjection(b *testing.B) {
	ctx, err := newContext(postgres.ProjectionDialect(),
		projection.WithAllowedFields("name", "email", "createTime"),
		projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
		projection.WithRequiredFields("id"),
	)
	require.NoError(b, err)

	spec := projection.Spec{Paths: []string{"createTime", "email", "name"}}
	for b.Loop() {
		_, _ = sqlbase.TranslateProjection(ctx, postgres.ProjectionDialect(), spec)
	}
}

func BenchmarkProjectionTranslator_Dialects(b *testing.B) {
	spec := projection.Spec{Paths: []string{"createTime", "email", "name"}}

	for _, d := range projectionDialects() {
		b.Run(d.name, func(b *testing.B) {
			tr, err := newTranslator(d.dialect,
				projection.WithAllowedFields("name", "email", "createTime"),
				projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
				projection.WithRequiredFields("id"),
			)
			require.NoError(b, err)
			b.ReportAllocs()

			for b.Loop() {
				_, _ = tr.Translate(spec)
			}
		})
	}
}
