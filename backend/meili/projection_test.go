// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/meili"
	"github.com/altessa-s/go-aip/metrics"
	"github.com/altessa-s/go-aip/projection"
	"github.com/altessa-s/go-aip/projection/projectiontest"
)

func TestProjectionConformance(t *testing.T) {
	t.Parallel()

	projectiontest.RunConformance(t, projectiontest.Backend{
		New: func(p *projection.Policy, storage ...projection.TranslatorOption) (projectiontest.Translator, error) {
			tr, err := meili.NewProjectionTranslator(p, meili.WithProjectionOptions(storage...))
			if err != nil {
				return nil, err
			}

			return projectiontest.Adapt(tr, decode), nil
		},
	})
}

func decode(attrs []string) (projectiontest.Result, error) {
	if attrs == nil {
		return projectiontest.Result{All: true}, nil
	}

	return projectiontest.Result{Include: attrs}, nil
}

func TestProjectionTranslate(t *testing.T) {
	t.Parallel()

	tr, err := meili.NewProjectionTranslator(projectiontest.MustPolicy(t, projection.WithAllowedFields("title", "author", "author.*", "createTime")),
		meili.WithProjectionOptions(
			projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
			projection.WithRequiredFields("id"),
		),
		meili.WithProjectionCollector(metrics.Noop()),
	)
	require.NoError(t, err)

	tests := []struct {
		name string
		spec projection.Spec
		want []string
	}{
		{name: "fields", spec: projection.Spec{Paths: []string{"title", "createTime"}}, want: []string{"created_at", "id", "title"}},
		{name: "nested", spec: projection.Spec{Paths: []string{"author.name"}}, want: []string{"author.name", "id"}},
		{name: "collapsed", spec: projection.Spec{Paths: []string{"author", "author.name"}}, want: []string{"author", "id"}},
		{name: "omitted", spec: projection.Spec{}, want: []string{"author", "created_at", "id", "title"}},
		{name: "none keeps required", spec: projection.Spec{Mode: projection.ModeNone}, want: []string{"id"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := tr.Translate(tc.spec)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestProjectionTranslateEdges(t *testing.T) {
	t.Parallel()

	tr, err := meili.NewProjectionTranslator(projectiontest.MustPolicy(t))
	require.NoError(t, err)

	got, err := tr.TranslateView(projection.Spec{}, projection.ViewFull)
	require.NoError(t, err)
	require.Nil(t, got, "every attribute omits the parameter")

	_, err = tr.Translate(projection.Spec{Mode: projection.ModeNone})
	require.ErrorIs(t, err, projection.ErrUnsupportedSelection, "an empty list would be dropped by omitempty and return everything")

	_, err = meili.NewProjectionTranslator(projectiontest.MustPolicy(t, projection.WithDeniedFields("password")))
	require.ErrorIs(t, err, projection.ErrAllFieldsUnresolvable)

	_, err = meili.NewProjectionTranslator(projectiontest.MustPolicy(t, projection.WithAllowedFields("title")),
		meili.WithProjectionOptions(projection.WithFieldMapping(map[string]string{"title": "*"})))
	require.ErrorIs(t, err, projection.ErrInvalidFieldPath, "a wildcard storage name would select every attribute")

	tr, err = meili.NewProjectionTranslator(projectiontest.MustPolicy(t, projection.WithAllowedFields("*")),
		meili.WithProjectionOptions(projection.WithFieldMapping(map[string]string{"nick": "ni ck"})))
	require.NoError(t, err)

	_, err = tr.Translate(projection.Spec{Paths: []string{"nick"}})
	require.ErrorIs(t, err, projection.ErrInvalidFieldPath)

	_, err = tr.TranslateView(projection.Spec{}, projection.View(42))
	require.ErrorIs(t, err, projection.ErrInvalidView)
}
