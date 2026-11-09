// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package functions builds pure AST expansions for custom filter functions.
//
// Helpers return CustomFunction AST expansions; constructing or importing this package registers nothing globally. Install handlers explicitly
// with parser.WithCustomFunctions. Expansion output is subject to the same field authorization, literal rules, AST limits, and backend
// compatibility checks as ordinary syntax.
//
// # Usage
//
//	search := functions.TextSearch([]string{"name", "description"})
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package functions
