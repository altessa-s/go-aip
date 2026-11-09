// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package parser parses CEL expressions into caller-owned or explicitly shared filter ASTs.
//
// New(opts...) builds a parser with its own CEL environment and function handlers; there is no global registration step or implicit preset
// registry. Supply expansions through WithCustomFunctions, including helpers from the sibling functions package. Named builtins can only be
// replaced with WithAllowBuiltinOverride(true).
//
// # Usage
//
//	p, err := parser.New(parser.WithNoCache(true))
//	node, err := p.Parse(ctx, `name == "Ada"`)
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package parser
