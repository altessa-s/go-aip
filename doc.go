// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package aip is the root of go-aip, helpers for building APIs that follow the
// Google API Improvement Proposals (https://google.aip.dev): resource names
// (AIP-122, AIP-123), List methods with ordering (AIP-132), partial responses
// (AIP-157), pagination (AIP-158), reads across collections (AIP-159) and
// filtering (AIP-160), field masks and field behavior (AIP-134, AIP-161,
// AIP-203), resource freshness (AIP-154), request identification (AIP-155),
// change validation (AIP-163), field formats (AIP-202), soft delete (AIP-164,
// AIP-165), errors (AIP-193), states (AIP-216), unreachable resources
// (AIP-217) and batch methods (AIP-231, AIP-233, AIP-234, AIP-235). The
// packages implementing them live in subdirectories of this module; every
// storage backend is a Go module of its own under backend/.
//
// See README.md for the package list and the AIP coverage, and
// docs/architecture.md for the layering.
package aip
