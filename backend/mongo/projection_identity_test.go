// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

// The MongoDB cases of the projection package's selection and access-policy
// tests.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/projection"
)

func TestSelectionModeNoneUnsupported(t *testing.T) {
	t.Parallel()

	tr, err := mongo.NewProjectionTranslator(mustPolicy(t))
	require.NoError(t, err)

	_, err = tr.Translate(projection.Spec{Mode: projection.ModeNone})
	require.ErrorIs(t, err, projection.ErrUnsupportedSelection)
}

func TestMongoIdentityPolicy(t *testing.T) {
	t.Parallel()

	tr, err := mongo.NewProjectionTranslator(mustPolicy(t), mongo.WithProjectionIDField("uid"), mongo.WithProjectionIncludeID(true), mongo.WithProjectionOptions(projection.WithRequiredFields("version")))
	require.NoError(t, err)

	got, err := tr.Translate(projection.Spec{Paths: []string{"name"}})
	require.NoError(t, err)
	require.Equal(t, int32(1), got["uid"])
	require.Equal(t, int32(1), got["version"])
}

func TestMongoCustomIDExclusion(t *testing.T) {
	t.Parallel()

	tr, err := mongo.NewProjectionTranslator(mustPolicy(t), mongo.WithProjectionIDField("uid"))
	require.NoError(t, err)

	got, err := tr.Translate(projection.Spec{Paths: []string{"name"}})
	require.NoError(t, err)
	require.NotContains(t, got, "uid")
	require.Equal(t, int32(0), got["_id"])
}

func TestMongoEmptyIdentityKeepsDefault(t *testing.T) {
	t.Parallel()

	tr, err := mongo.NewProjectionTranslator(mustPolicy(t), mongo.WithProjectionIncludeID(true), mongo.WithProjectionIDField(""))
	require.NoError(t, err)

	got, err := tr.Translate(projection.Spec{Paths: []string{"name"}})
	require.NoError(t, err)
	require.Equal(t, int32(1), got["_id"])
}
