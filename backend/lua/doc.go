// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package lua is the Lua backend: its one kind, [FilterTranslator], converts filter AST nodes to Lua boolean expressions for
// use in Redis EVAL scripts. Ordering and projection have no Lua form; the redis backend provides them.
//
// [NewFilterTranslator] takes the shared *filter.Policy (field authorization, types, limits, decimal conversion, value codecs and regex
// rules, in API names, built once per resource) and this package's [FilterOption] values. WithFilterOptions binds the policy to storage names
// (filter.WithFieldMapping); WithFilterTableVar names the Lua variable holding the record (default "d"). Fields declared with
// filter.WithArrayFields are tested element by element. A nil policy fails with filter.ErrPolicyRequired; filter.NewPolicy
// requires an allowlist or an explicit trusted WithAnyField opt-out.
//
// # Usage
//
//	parser, _ := filterparser.New()
//	ast, _ := parser.Parse(ctx, `name == "John" && age >= 18`)
//
//	policy, err := filter.NewPolicy(filter.WithAllowedFields("name", "age"))
//	if err != nil {
//	    return err
//	}
//	trans, err := lua.NewFilterTranslator(policy)
//	if err != nil {
//	    return err
//	}
//	expr, _ := trans.Translate(ast)
//	// Result: `((d["name"] == "John") and (type(d["age"]) == "number" and d["age"] >= 18))`
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package lua
