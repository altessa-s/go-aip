// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package sqltype holds the type checker and the literal value model of the schema-aware SQL filter translators: the
// dialect SPI that backend/postgres, backend/mariadb and backend/clickhouse build on, and that a SQL dialect outside this
// repository uses the same way. It builds on [sqlbase] (operand kinds, substring bounds) and is imported only by dialects.
//
// # Type checker
//
// [Checker] types a complete filter tree against a schema before anything is rendered and returns a typed tree of [Expr]
// nodes that mirrors the AST: the kind and array shape of every operand, the validated literal values, the dimensions of
// nested lists, the bounds of substring() and the compiled pattern of matches(). A dialect plugs in through [Dialect]
// (field storage, literal validation, one extra binary check and the regex check) and the switches of [Checker] (contract
// `in`, nested lists, decimal normalization, error precedence and literal string arguments). The translators render from
// the typed tree, so the renderer and the checker agree on every kind.
//
// # Literal values
//
// [ParseNumber] reads an exact SQL NUMERIC [Number] (finite decimals within PostgreSQL's unconstrained NUMERIC bounds, NaN
// and the infinities), [Integer] and [Float] narrow it to BIGINT and DOUBLE, and [Unwrap] follows pointers and driver
// decoders to the value they hold. Every step charges a [Budget], which also supplies the dialect's operand type error.
//
// # Usage
//
//	c := sqltype.Checker[sqlbase.Field]{Dialect: dialect, Policy: policy, TypeError: typeError, Unsupported: unsupported,
//		Contract: true, MaxArrayDimensions: 6}
//	root, err := c.Check(node) // node is a non-nil filter.Node
//	if err != nil {
//		return err
//	}
//	// render SQL from root: every operand carries its kind, array shape and validated value
//
// See README.md for the complete guide.
package sqltype
