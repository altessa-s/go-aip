// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/projection"
)

// newProjectionTranslator builds a policy from the policy options in opts and a
// translator with the translator options, so tests can list both kinds.
func newProjectionTranslator(tb testing.TB, opts ...any) *mongo.ProjectionTranslator {
	tb.Helper()

	var (
		policy  []projection.PolicyOption
		storage []projection.TranslatorOption
	)

	for _, opt := range opts {
		switch opt := opt.(type) {
		case projection.PolicyOption:
			policy = append(policy, opt)
		case projection.TranslatorOption:
			storage = append(storage, opt)
		default:
			tb.Fatalf("unexpected option %T", opt)
		}
	}

	tr, err := mongo.NewProjectionTranslator(mustPolicy(tb, policy...), mongo.WithProjectionOptions(storage...))
	require.NoError(tb, err)

	return tr
}

func mustPolicy(tb testing.TB, opts ...projection.PolicyOption) *projection.Policy {
	tb.Helper()

	p, err := projection.NewPolicy(opts...)
	require.NoError(tb, err)

	return p
}

func TestProjectionTranslate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    []any
		paths   []string
		want    bson.M
		wantErr error
	}{
		{name: "all fields without policy", want: nil},
		{
			name:  "inclusion suppresses _id",
			paths: []string{"name", "email"},
			want:  bson.M{"name": int32(1), "email": int32(1), "_id": int32(0)},
		},
		{
			name:  "required _id is kept",
			opts:  []any{projection.WithRequiredFields("_id")},
			paths: []string{"name"},
			want:  bson.M{"name": int32(1), "_id": int32(1)},
		},
		{
			name:  "_id subpath keeps _id unsuppressed",
			paths: []string{"_id.tenant"},
			want:  bson.M{"_id.tenant": int32(1)},
		},
		{
			name:  "parent and child collapse",
			paths: []string{"address", "address.city", "addressee"},
			want:  bson.M{"address": int32(1), "addressee": int32(1), "_id": int32(0)},
		},
		{
			name: "collision created by mapping collapses",
			opts: []any{projection.WithFieldMapping(map[string]string{
				"city": "addr.city", "address": "addr",
			})},
			paths: []string{"city", "address"},
			want:  bson.M{"addr": int32(1), "_id": int32(0)},
		},
		{
			name:  "collision created by required field collapses",
			opts:  []any{projection.WithRequiredFields("meta")},
			paths: []string{"meta.version"},
			want:  bson.M{"meta": int32(1), "_id": int32(0)},
		},
		{
			name: "omitted mask excludes denied fields",
			opts: []any{
				projection.WithDeniedFields("password"),
				projection.WithDeniedStorageFields("tokens", "tokens.refresh"),
			},
			want: bson.M{"password": int32(0), "tokens": int32(0)},
		},
		{
			name: "omitted mask uses allow-list roots",
			opts: []any{projection.WithAllowedFields("name", "address.*")},
			want: bson.M{"name": int32(1), "address": int32(1), "_id": int32(0)},
		},
		{
			name:    "denied ancestor request rejected",
			opts:    []any{projection.WithDeniedFields("credentials.password")},
			paths:   []string{"credentials"},
			wantErr: projection.ErrFieldNotAllowed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := newProjectionTranslator(t, tc.opts...).Translate(projection.Spec{Paths: tc.paths})
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestNewProjectionTranslatorPropagatesPolicyErrors(t *testing.T) {
	t.Parallel()

	_, err := projection.NewPolicy(projection.WithUntrustedInput(true))
	require.ErrorIs(t, err, projection.ErrAllowlistRequired)

	_, err = mongo.NewProjectionTranslator(nil)
	require.ErrorIs(t, err, projection.ErrPolicyRequired)

	_, err = mongo.NewProjectionTranslator(mustPolicy(t, projection.WithDeniedFields("secret")),
		mongo.WithProjectionOptions(projection.WithRequiredFields("secret")))
	require.ErrorIs(t, err, projection.ErrConflictingPolicy)
}

func TestProjectionTranslateReturnsFreshDocument(t *testing.T) {
	t.Parallel()

	tr := newProjectionTranslator(t, projection.WithAllowedFields("name"))
	first, err := tr.Translate(projection.Spec{})
	require.NoError(t, err)

	first["injected"] = int32(1)

	second, err := tr.Translate(projection.Spec{})
	require.NoError(t, err)
	require.NotContains(t, second, "injected")
}
