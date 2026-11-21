// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/drivertest"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/xoptions"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"

	driver "go.mongodb.org/mongo-driver/v2/mongo"
)

func TestMigrationListToken(t *testing.T) {
	t.Parallel()

	// Script only the server replies. Both reads go through the driver's command
	// encoder and the engine's normal token issue, resolve and position decoding.
	doc := func(rank int32, seq int64) bson.D {
		return bson.D{{Key: "rank", Value: rank}, {Key: "identity", Value: bson.D{{Key: "seq", Value: seq}}}}
	}
	reply := func(batch bson.A) bson.D {
		return bson.D{{Key: "ok", Value: 1}, {Key: "cursor", Value: bson.D{
			{Key: "id", Value: int64(0)}, {Key: "ns", Value: "fixtures.items"}, {Key: "firstBatch", Value: batch},
		}}}
	}
	server := drivertest.NewMockDeployment(reply(bson.A{doc(7, 42), doc(7, 43)}), reply(bson.A{doc(7, 43)}))

	var predicates []bson.Raw

	clientOpts := options.Client().SetMonitor(&event.CommandMonitor{
		Started: func(_ context.Context, e *event.CommandStartedEvent) {
			if e.CommandName == "find" {
				predicates = append(predicates, bytes.Clone(e.Command.Lookup("filter").Document()))
			}
		},
	})
	require.NoError(t, xoptions.SetInternalClientOptions(clientOpts, "deployment", server))
	client, err := driver.Connect(clientOpts)

	require.NoError(t, err)
	defer func() { require.NoError(t, client.Disconnect(t.Context())) }()

	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	signer, err := pagetoken.New(bytes.Repeat([]byte{0x42}, 32), pagetoken.WithClock(func() time.Time { return now }))
	require.NoError(t, err)

	fields := []listing.Field{{Name: "rank", Kind: filter.FieldKindInt}, {Name: "seq", Kind: filter.FieldKindInt}}
	schema, err := listing.NewSchema(fields, []string{"rank desc, seq"}, listing.NewProfile())
	require.NoError(t, err)
	engine, err := mongo.NewListEngine[bson.D](client.Database("fixtures").Collection("items"), signer, schema,
		mongo.WithListFieldPaths(map[string]string{"seq": "identity.seq"}), mongo.WithListTiebreakPath("identity.seq"))
	require.NoError(t, err)

	scope := bson.D{{Key: "tenant", Value: "tenant-17"}}
	req := listing.Request{PageSize: 1}
	first, err := engine.List(t.Context(), "fixture-reader", req, scope)
	require.NoError(t, err)

	var oldToken string
	readMigrationJSON(t, "testdata/backend-list-token.json", &oldToken)
	require.Equal(t, oldToken, first.NextPageToken)
	req.PageToken = oldToken
	second, err := engine.List(t.Context(), "fixture-reader", req, scope)
	require.NoError(t, err)
	require.Equal(t, []bson.D{doc(7, 43)}, second.Items)
	require.Empty(t, second.NextPageToken)

	// A descending key also reads the documents whose sort value is null, which sort after every value (go-mongox v0.1.1).
	wantSeek := bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: "rank", Value: bson.D{{Key: "$lt", Value: int32(7)}}}},
			bson.D{{Key: "rank", Value: nil}},
		}}},
		bson.D{{Key: "rank", Value: bson.D{{Key: "$eq", Value: int32(7)}}}, {Key: "identity.seq", Value: bson.D{{Key: "$gt", Value: int64(42)}}}},
	}}}
	want, err := bson.Marshal(bson.D{{Key: "$and", Value: bson.A{scope, wantSeek}}})
	require.NoError(t, err)
	require.Len(t, predicates, 2)
	require.Equal(t, bson.Raw(want), predicates[1])
}
