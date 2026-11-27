// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package postgres_test

import (
	"encoding/base64"
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/apierror"
	"github.com/altessa-s/go-aip/apierror/aipmap"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestNotificationVisibilityRestriction(t *testing.T) {
	t.Parallel()

	kinds := []string{
		"NOTIFICATION_KIND_DIGEST_FAILED", "NOTIFICATION_KIND_SOURCE_ATTENTION", "NOTIFICATION_KIND_DISCOVERY_NEW",
		"NOTIFICATION_KIND_BUDGET_WARNING", "NOTIFICATION_KIND_TASK_FAILED", "NOTIFICATION_KIND_RADAR_ARTICLE",
	}

	const (
		hiddenKind = "NOTIFICATION_KIND_UNSPECIFIED"
		subject    = "notifications"
	)

	db := dbtest.Postgres17(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE notifications (id text COLLATE "C" PRIMARY KEY, kind text COLLATE "C" NOT NULL)`)
	require.NoError(t, err)

	enum := map[string]any{hiddenKind: hiddenKind}
	ids := make([]string, len(kinds))

	for i, kind := range kinds {
		enum[kind] = kind
		ids[i] = strconv.Itoa(i)

		_, err := db.ExecContext(t.Context(), `INSERT INTO notifications VALUES ($1, $2)`, ids[i], kind)
		require.NoError(t, err)
	}

	// The membership predicate excludes 0-hidden and the client filter excludes 7.
	_, err = db.ExecContext(t.Context(), `INSERT INTO notifications VALUES ('6', $1), ('0-hidden', $2), ('7', $1)`,
		kinds[0], hiddenKind)
	require.NoError(t, err)

	schema := listing.MustSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "kind", Kind: filter.FieldKindString, Enum: enum},
	}, []string{"id"}, listing.NewProfile())
	engine, err := postgres.NewListEngine(db, pagetokentest.Codec(t), schema, "notifications", []string{"id"},
		func(id *string) []any { return []any{id} })
	require.NoError(t, err)
	require.NoError(t, engine.CheckSchema(t.Context(), db))

	for size := 1; size <= len(kinds); size++ {
		t.Run(strconv.Itoa(size)+" visible kinds", func(t *testing.T) {
			t.Parallel()

			req := listing.Request{PageSize: 1, Filter: `id != "7"`, Restriction: notificationKindsIn(kinds[:size]...)}
			want := append(slices.Clone(ids[:size]), "6")

			for i, id := range want {
				p, err := engine.List(t.Context(), subject, req, nil)
				require.NoError(t, err)
				require.Equal(t, []string{id}, p.Items)

				if i < len(want)-1 {
					require.NotEmpty(t, p.NextPageToken)
				} else {
					require.Empty(t, p.NextPageToken)
				}

				total, err := engine.Count(t.Context(), req, nil)
				require.NoError(t, err)
				require.Equal(t, int64(len(want)), total)

				req.PageToken = p.NextPageToken
			}
		})
	}

	mapper, err := apierror.NewMapper(apierror.WithRules(aipmap.Rules()...))
	require.NoError(t, err)

	req := listing.Request{PageSize: 1, Filter: `id != "7"`, Restriction: notificationKindsIn(kinds[:2]...)}
	first, err := engine.List(t.Context(), subject, req, nil)
	require.NoError(t, err)
	require.NotEmpty(t, first.NextPageToken)

	t.Run("changed visibility invalidates token", func(t *testing.T) {
		t.Parallel()

		changed := req
		changed.PageToken = first.NextPageToken
		changed.Restriction = notificationKindsIn(kinds[0], kinds[2])
		_, err := engine.List(t.Context(), subject, changed, nil)
		require.ErrorIs(t, err, pagetoken.ErrFilterChanged)
		require.Equal(t, aipmap.ReasonInvalidPageToken, mapper.Map(err).Reason())
	})

	t.Run("zero visible kinds", func(t *testing.T) {
		t.Parallel()

		kind := &filter.IdentNode{Name: "kind"}
		value := &filter.LiteralNode{Value: kinds[0]}
		none := req
		none.Restriction = &filter.BinaryOpNode{Op: filter.OpAnd,
			Left:  &filter.BinaryOpNode{Op: filter.OpEqual, Left: kind, Right: value},
			Right: &filter.BinaryOpNode{Op: filter.OpNotEqual, Left: kind, Right: value},
		}
		p, err := engine.List(t.Context(), subject, none, nil)
		require.NoError(t, err)
		require.Empty(t, p.Items)
		require.Empty(t, p.NextPageToken)

		total, err := engine.Count(t.Context(), none, nil)
		require.NoError(t, err)
		require.Zero(t, total)

		badFilter := none
		badFilter.Filter = `unknownKind == "NOTIFICATION_KIND_DIGEST_FAILED"`
		_, err = engine.List(t.Context(), subject, badFilter, nil)

		var fieldErr *coreerrs.FieldError
		require.ErrorAs(t, err, &fieldErr)
		require.Equal(t, listing.FieldFilter, fieldErr.Field)

		_, err = engine.Count(t.Context(), badFilter, nil)
		require.ErrorAs(t, err, &fieldErr)
		require.Equal(t, listing.FieldFilter, fieldErr.Field)

		none.PageToken = first.NextPageToken
		_, err = engine.List(t.Context(), subject, none, nil)
		require.ErrorIs(t, err, pagetoken.ErrFilterChanged)

		raw, err := base64.RawURLEncoding.DecodeString(first.NextPageToken)
		require.NoError(t, err)

		raw[len(raw)-1] ^= 1
		none.PageToken = base64.RawURLEncoding.EncodeToString(raw)
		_, err = engine.List(t.Context(), subject, none, nil)
		require.ErrorIs(t, err, pagetoken.ErrInvalidToken)
		require.Equal(t, aipmap.ReasonInvalidPageToken, mapper.Map(err).Reason())
	})
}

func notificationKindsIn(kinds ...string) filter.Node {
	elements := make([]filter.Node, len(kinds))
	for i, kind := range kinds {
		elements[i] = &filter.LiteralNode{Value: kind}
	}

	return &filter.BinaryOpNode{Op: filter.OpIn, Left: &filter.IdentNode{Name: "kind"}, Right: &filter.ListNode{Elements: elements}}
}
