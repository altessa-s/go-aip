// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestSchemaSnapshots(t *testing.T) {
	t.Parallel()

	path := []string{"items", "-1"}
	fields := map[string]sqlbase.Field{
		"city": sqlbase.JSONPath("account.payload", sqlbase.Text, path...),
		"tags": sqlbase.Array("tags", sqlbase.Text),
		"note": sqlbase.PresenceScalar("note", sqlbase.Text),
	}
	path[0] = "changed"
	schema, err := sqlbase.NewSchema(fields)
	require.NoError(t, err)
	clear(fields)

	f, err := schema.Field("city")
	require.NoError(t, err)
	require.Equal(t, "account.payload", f.Column())
	require.Equal(t, sqlbase.Text, f.Kind())
	require.True(t, f.IsJSONPath())
	got := f.Path()
	got[0] = "changed again"

	require.Equal(t, []string{"items", "-1"}, f.Path())

	f, err = schema.Field("tags")
	require.NoError(t, err)
	require.True(t, f.IsArray())
	require.False(t, f.HasPresence())
	require.False(t, f.IsJSONPath())

	f, err = schema.Field("note")
	require.NoError(t, err)
	require.True(t, f.HasPresence())

	_, err = schema.Field("missing")
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
}

func TestInvalidDescriptors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		field sqlbase.Field
		want  error
	}{
		{"", sqlbase.Scalar("name", sqlbase.Text), filter.ErrInvalidOptions},
		{"zero", sqlbase.Field{}, filter.ErrInvalidOptions},
		{"unknown", sqlbase.Scalar("name", sqlbase.Unknown), filter.ErrInvalidOptions},
		{"kind", sqlbase.Scalar("name", sqlbase.Kind(255)), filter.ErrInvalidOptions},
		{"expression", sqlbase.Scalar("lower(name)", sqlbase.Text), filter.ErrInvalidExpression},
		{"qualified", sqlbase.Scalar("t..name", sqlbase.Text), filter.ErrInvalidExpression},
		{"nul", sqlbase.JSONPath("doc", sqlbase.Text, "\x00"), filter.ErrInvalidOptions},
		{"utf8", sqlbase.JSONPath("doc", sqlbase.Text, "\xff"), filter.ErrInvalidOptions},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := sqlbase.NewSchema(map[string]sqlbase.Field{tc.name: tc.field})
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestPolicyValidation(t *testing.T) {
	t.Parallel()

	schema, err := sqlbase.NewSchema(map[string]sqlbase.Field{"name": sqlbase.Scalar("t.name", sqlbase.Text)})
	require.NoError(t, err)

	for _, tc := range []struct {
		opts    []filter.PolicyOption
		binding []filter.TranslatorOption
	}{
		{binding: []filter.TranslatorOption{filter.WithFieldMapping(map[string]string{"name": "other"})}},
		{opts: []filter.PolicyOption{filter.WithFieldTypes(map[string]filter.FieldKind{"name": filter.FieldKindInt})}},
		{opts: []filter.PolicyOption{filter.WithArrayFields("name")}},
	} {
		policy, err := filter.NewTranslatorContext(filtertest.MustPolicy(t, append(tc.opts, filter.WithAnyField(true))...), tc.binding...)
		require.NoError(t, err)
		require.ErrorIs(t, schema.ValidatePolicy(policy), filter.ErrInvalidOptions)
	}

	policy, err := filter.NewTranslatorContext(filtertest.MustPolicy(t, filter.WithAnyField(true)), filter.WithFieldMapping(map[string]string{"name": "t.name"}))
	require.NoError(t, err)
	require.NoError(t, schema.ValidatePolicy(policy))
	require.ErrorIs(t, schema.ValidatePolicy(nil), filter.ErrInvalidOptions)

	var missing *sqlbase.Schema
	require.ErrorIs(t, missing.ValidatePolicy(policy), filter.ErrInvalidOptions)
	_, err = missing.Field("name")
	require.ErrorIs(t, err, filter.ErrInvalidOptions)
}
