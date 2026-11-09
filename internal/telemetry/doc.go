// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package telemetry creates the parse metrics shared by the filter, orderby and projection parsers.
//
// This is an internal implementation package. Its exported helpers are available only to packages permitted by Go internal import rules.
// Every parser records the same two instruments under its own subsystem, so the names and help texts are built here once.
//
// # Usage
//
//	m := telemetry.NewParser(collector, "orderby", "order_by") // collector is a metrics.Meter, nil means metrics.Noop
//	stop := m.Duration.Start()
//	defer stop()
//
// See README.md for the complete guide.
package telemetry
