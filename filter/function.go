// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

// CustomFunction expands a CEL call of the form name(args...) into an
// arbitrary AST node. The handler receives already converted argument
// nodes and is responsible for validating arity and types. The node it
// returns replaces the original call in the AST before the filter
// reaches any evaluator.Evaluator or translator.
//
// See parser.WithCustomFunctions for registration and functions.CompareField for the
// canonical "field op arg" shortcut.
type CustomFunction func(args []Node) (Node, error)
