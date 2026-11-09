// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package behavior reads the google.api.field_behavior annotation from a protoreflect.FieldDescriptor and holds the field paths and
// the REQUIRED, IMMUTABLE and IDENTIFIER checks shared by the field mask and field behavior packages.
//
// This is an internal implementation package. Its exported helpers are available only to packages permitted by Go internal import rules.
// Mutable work state belongs to the caller or an explicitly constructed instance; consumers should use the public parent APIs.
//
// # Usage
//
//	path := behavior.JoinPath("labels", "google.com/project")
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package behavior
