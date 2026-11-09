// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package typename spells the type of a value in an error message, bounded: the value may come from the data, and the name of a
// struct type constructed at run time (reflect.StructOf) lists all its fields and their tags.
//
// This is an internal implementation package. Its exported helpers are available only to packages permitted by Go internal import rules.
//
// # Usage
//
//	err := fmt.Errorf("cannot compare %s", typename.Of(v)) // %T, cut to 128 bytes
//
// See README.md for the complete guide.
package typename
