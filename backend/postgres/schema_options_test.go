// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestSemanticOptions(t *testing.T) {
	t.Parallel()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar("name", sqlbase.Text), "tags": sqlbase.Array("tags", sqlbase.Text), "at": sqlbase.Scalar("at", sqlbase.Timestamp)})
	require.NoError(t, err)
	_, err = postgres.NewSchema(fields, postgres.WithSchemaTextCollation("application"))
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
	s, err := postgres.NewSchema(fields, postgres.WithSchemaTextCollation("application"), postgres.WithSchemaTextCompare(func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) }), postgres.WithSchemaArrayAliasPrefix("item"), postgres.WithSchemaTimestampPrecision(time.Second))
	require.NoError(t, err)
	p := compile(t, s, `name == "Ada"`)
	matched, err := p.Evaluate(map[string]any{"name": "ADA"}, postgres.DefaultMaxDataBudget, nil)
	require.NoError(t, err)
	require.True(t, matched)

	query, _, err := p.SQL()
	require.NoError(t, err)
	require.Contains(t, query, `COLLATE "application"`)
	query, _, err = compile(t, s, `tags.contains("a")`).SQL()
	require.NoError(t, err)
	require.Contains(t, query, `AS "item0"`)

	policy, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)

	instant := time.Date(2026, 1, 1, 0, 0, 0, 123456789, time.UTC)
	value, present, err := s.Lookup(policy, map[string]any{"at": instant}, "at", postgres.DefaultMaxDataBudget, nil)
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, instant.Truncate(time.Second), value)

	limited, err := postgres.NewSchema(fields, postgres.WithSchemaMaxDataBudget(1))
	require.NoError(t, err)
	_, err = limited.Compile(policy, filtertest.MustParseFilter(t, `name == "long literal"`))
	require.ErrorIs(t, err, filter.ErrDataLimitExceeded)
}

func TestTimestampPrecisionZero(t *testing.T) {
	t.Parallel()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"at": sqlbase.Scalar("at", sqlbase.Timestamp)})
	require.NoError(t, err)
	s, err := postgres.NewSchema(fields, postgres.WithSchemaTimestampPrecision(0))
	require.NoError(t, err)
	policy, err := filter.NewPolicy(filter.WithAnyField(true))
	require.NoError(t, err)

	at := time.Unix(0, 123456789).UTC()
	got, present, err := s.Lookup(policy, map[string]any{"at": at}, "at", postgres.DefaultMaxDataBudget, nil)
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, at, got)
}
