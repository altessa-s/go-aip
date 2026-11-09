// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package dbtest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"go.mongodb.org/mongo-driver/v2/mongo"

	backendmongo "github.com/altessa-s/go-aip/backend/mongo"
	tcmongodb "github.com/testcontainers/testcontainers-go/modules/mongodb"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

// mongoServer is the MongoDB server of the test binary: a single standalone node, started on first use.
var mongoServer = &mongoEngine{}

// mongoEngine is the started MongoDB server: its container, its connection string and a client of it.
type mongoEngine struct {
	once      sync.Once
	container testcontainers.Container
	uri       string
	client    *mongo.Client
	err       error
	skip      string
}

func (m *mongoEngine) label() string { return "MongoDB" }

// boot starts the server once, recording why it cannot when Docker is unreachable.
func (m *mongoEngine) boot() {
	if reason := dockerUnavailable(); reason != "" {
		m.skip = reason

		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	ctr, err := tcmongodb.Run(ctx, MongoImage)
	if ctr != nil {
		m.container = ctr

		startedMu.Lock()

		started = append(started, m)
		startedMu.Unlock()
	}

	if err != nil {
		m.err = err

		return
	}

	if m.uri, err = ctr.ConnectionString(ctx); err != nil {
		m.err = err

		return
	}

	client, err := mongo.Connect(mongoClientOptions(m.uri))
	if err == nil {
		err = client.Ping(ctx, nil)
	}

	if err != nil {
		m.err = fmt.Errorf("connect: %w", err)

		return
	}

	m.client = client
}

// stop disconnects the client and terminates the container.
func (m *mongoEngine) stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()

	var errs []error
	if m.client != nil {
		errs = append(errs, m.client.Disconnect(ctx))
	}

	if m.container != nil {
		errs = append(errs, m.container.Terminate(ctx))
	}

	return errors.Join(errs...)
}

// ready starts the server on first use, skipping tb when Docker is unreachable and failing it when the server cannot start.
func (m *mongoEngine) ready(tb testing.TB) {
	tb.Helper()

	m.once.Do(m.boot)

	if m.skip != "" {
		tb.Skipf("dbtest: MongoDB needs Docker: %s", m.skip)
	}

	if m.err != nil {
		tb.Fatalf("dbtest: start MongoDB: %v", m.err)
	}
}

// mongoClientOptions are the options of the clients of the server: the backend/mongo codec registry, so decimals, optional
// values and civil dates are stored the way the MongoDB backend reads them.
func mongoClientOptions(uri string) *mongooptions.ClientOptions {
	return mongooptions.Client().ApplyURI(uri).SetRegistry(backendmongo.NewRegistry())
}

// Mongo returns a new, empty database of a MongoDB 8.0 server (a client with the backend/mongo registry) that is dropped when
// tb ends. The server starts on the first call of the test binary. It skips tb when Docker is unreachable.
func Mongo(tb testing.TB) *mongo.Database {
	tb.Helper()

	mongoServer.ready(tb)

	name := fmt.Sprintf("aip_%06d", databases.Add(1))
	db := mongoServer.client.Database(name)

	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()

		if err := db.Drop(ctx); err != nil {
			tb.Errorf("dbtest: drop MongoDB database %s: %v", name, err)
		}
	})

	return db
}

// MongoClientOptions returns the options of a new client of the MongoDB server (its connection string and the backend/mongo
// registry), for a test that needs a client of its own, such as one with a command monitor. The server starts on the first
// call of the test binary. It skips tb when Docker is unreachable.
func MongoClientOptions(tb testing.TB) *mongooptions.ClientOptions {
	tb.Helper()

	mongoServer.ready(tb)

	return mongoClientOptions(mongoServer.uri)
}
