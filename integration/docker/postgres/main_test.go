// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package postgres_test

import (
	"os"
	"testing"

	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
)

// TestMain stops the PostgreSQL container the integration tests start.
func TestMain(m *testing.M) { os.Exit(dbtest.Run(m)) }
