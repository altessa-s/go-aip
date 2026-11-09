// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package docker holds the live database tests of go-aip: the PostgreSQL, MariaDB, ClickHouse and
// MongoDB backends run against real servers started with testcontainers-go. It is a test-only Go module,
// never released, so the Docker and driver dependencies stay out of the go-aip module. The tests
// build only with the integration_docker build tag and need a Docker daemon; see README.md.
package docker
