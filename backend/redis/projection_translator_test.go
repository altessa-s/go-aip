// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis_test

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/redis"
	"github.com/altessa-s/go-aip/metrics"
	"github.com/altessa-s/go-aip/projection"
	"github.com/altessa-s/go-aip/projection/projectiontest"
)

func TestProjectionConformance(t *testing.T) {
	t.Parallel()

	projectiontest.RunConformance(t, projectiontest.Backend{
		New: func(p *projection.Policy, storage ...projection.TranslatorOption) (projectiontest.Translator, error) {
			tr, err := redis.NewProjectionTranslator(p, redis.WithProjectionOptions(storage...))
			if err != nil {
				return nil, err
			}

			return projectiontest.Adapt(tr, decode), nil
		},
		Capabilities: projectiontest.Capabilities{None: true},
	})
}

// decode reads RETURN arguments back into attribute names.
func decode(args []string) (projectiontest.Result, error) {
	if args == nil {
		return projectiontest.Result{All: true}, nil
	}

	if len(args) < 2 || args[0] != redis.Return {
		return projectiontest.Result{}, fmt.Errorf("malformed RETURN clause %q", args)
	}

	n, err := strconv.Atoi(args[1])
	if err != nil || n != len(args)-2 {
		return projectiontest.Result{}, fmt.Errorf("RETURN count %q does not match %d fields", args[1], len(args)-2)
	}

	if n == 0 {
		return projectiontest.Result{None: true}, nil
	}

	return projectiontest.Result{Include: args[2:]}, nil
}

func TestProjectionTranslate(t *testing.T) {
	t.Parallel()

	tr, err := redis.NewProjectionTranslator(projectiontest.MustPolicy(t, projection.WithAllowedFields("name", "city", "createTime")),
		redis.WithProjectionOptions(
			projection.WithFieldMapping(map[string]string{"city": "$.address.city", "createTime": "created_at"}),
			projection.WithRequiredFields("id"),
		),
		redis.WithProjectionCollector(metrics.Noop()),
	)
	require.NoError(t, err)

	tests := []struct {
		name string
		spec projection.Spec
		want []string
	}{
		{name: "fields", spec: projection.Spec{Paths: []string{"name", "createTime"}}, want: []string{"RETURN", "3", "created_at", "id", "name"}},
		{name: "json path", spec: projection.Spec{Paths: []string{"city"}}, want: []string{"RETURN", "2", "$.address.city", "id"}},
		{name: "omitted", spec: projection.Spec{}, want: []string{"RETURN", "4", "$.address.city", "created_at", "id", "name"}},
		{name: "none keeps required", spec: projection.Spec{Mode: projection.ModeNone}, want: []string{"RETURN", "1", "id"}},
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

func TestProjectionTranslateNone(t *testing.T) {
	t.Parallel()

	tr, err := redis.NewProjectionTranslator(projectiontest.MustPolicy(t))
	require.NoError(t, err)

	got, err := tr.Translate(projection.Spec{Mode: projection.ModeNone})
	require.NoError(t, err)
	require.Equal(t, []string{"RETURN", "0"}, got, "no field returns the document keys only")

	got, err = tr.TranslateView(projection.Spec{}, projection.ViewFull)
	require.NoError(t, err)
	require.Nil(t, got, "every field appends no RETURN clause")
}

func TestProjectionNewTranslatorErrors(t *testing.T) {
	t.Parallel()

	_, err := redis.NewProjectionTranslator(projectiontest.MustPolicy(t, projection.WithDeniedFields("password")))
	require.ErrorIs(t, err, projection.ErrAllFieldsUnresolvable)

	_, err = redis.NewProjectionTranslator(projectiontest.MustPolicy(t, projection.WithAllowedFields("name")),
		redis.WithProjectionOptions(projection.WithFieldMapping(map[string]string{"name": "AS"})))
	require.ErrorIs(t, err, projection.ErrInvalidFieldPath, "AS is the alias keyword of RETURN")

	_, err = redis.NewProjectionTranslator(projectiontest.MustPolicy(t, projection.WithAllowedFields("name"), projection.WithView(projection.ViewBasic, "name")),
		redis.WithProjectionOptions(projection.WithFieldMapping(map[string]string{"name": "full name"})))
	require.ErrorIs(t, err, projection.ErrInvalidFieldPath)

	tr, err := redis.NewProjectionTranslator(projectiontest.MustPolicy(t, projection.WithAllowedFields("*")),
		redis.WithProjectionOptions(projection.WithFieldMapping(map[string]string{"nick": "ni\tck"})))
	require.NoError(t, err)

	_, err = tr.Translate(projection.Spec{Paths: []string{"nick"}})
	require.ErrorIs(t, err, projection.ErrInvalidFieldPath)

	_, err = tr.TranslateView(projection.Spec{}, projection.View(42))
	require.ErrorIs(t, err, projection.ErrInvalidView)
}
