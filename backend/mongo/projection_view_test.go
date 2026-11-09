// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/projection"
)

func TestProjectionTranslateView(t *testing.T) {
	t.Parallel()

	tr := newProjectionTranslator(t,
		projection.WithAllowedFields("name", "email", "createTime"),
		projection.WithView(projection.ViewBasic, "name"),
		projection.WithDefaultView(projection.ViewBasic),
		projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
		projection.WithRequiredFields("_id"),
	)
	all := bson.M{"_id": int32(1), "created_at": int32(1), "email": int32(1), "name": int32(1)}
	basic := bson.M{"_id": int32(1), "name": int32(1)}

	got, err := tr.Translate(projection.Spec{})
	require.NoError(t, err)
	require.Equal(t, all, got, "an omitted mask returns every authorized field, not the default view")

	tests := []struct {
		name string
		spec projection.Spec
		view projection.View
		want bson.M
	}{
		{name: "unspecified takes the default view", want: basic},
		{name: "basic", view: projection.ViewBasic, want: basic},
		{name: "full", view: projection.ViewFull, want: all},
		{name: "star wins over the view", spec: projection.Spec{Mode: projection.ModeAll}, view: projection.ViewBasic, want: all},
		{name: "mask wins over the view", spec: projection.Spec{Paths: []string{"email"}}, view: projection.ViewBasic, want: bson.M{"_id": int32(1), "email": int32(1)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := tr.TranslateView(tc.spec, tc.view)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	_, err = tr.TranslateView(projection.Spec{}, projection.ViewFull+1)
	require.ErrorIs(t, err, projection.ErrInvalidView)
	_, err = tr.TranslateView(projection.Spec{Paths: []string{"tags.0"}}, projection.ViewBasic)
	require.ErrorIs(t, err, projection.ErrUnsupportedPath, "an index path is an INVALID_ARGUMENT error")
	got, err = tr.TranslateView(projection.Spec{Mode: projection.ModeNone}, projection.ViewFull)
	require.NoError(t, err)
	require.Equal(t, bson.M{"_id": int32(1)}, got, "an empty selection keeps the required fields")
}

// TestProjectionTranslateViewWithoutAllowList renders FULL as an exclusion of the
// denied fields and a view as an inclusion.
func TestProjectionTranslateViewWithoutAllowList(t *testing.T) {
	t.Parallel()

	tr := newProjectionTranslator(t,
		projection.WithDeniedFields("password"),
		projection.WithView(projection.ViewBasic, "name"),
	)

	got, err := tr.TranslateView(projection.Spec{}, projection.ViewUnspecified)
	require.NoError(t, err)
	require.Equal(t, bson.M{"password": int32(0)}, got, "the FULL default excludes the denied fields")

	got, err = tr.TranslateView(projection.Spec{}, projection.ViewBasic)
	require.NoError(t, err)
	require.Equal(t, bson.M{"name": int32(1), "_id": int32(0)}, got)
}
