// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package functions

import (
	"regexp"
	"slices"
	"strings"

	filter "github.com/altessa-s/go-aip/filter"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// TextSearch returns a [filter.CustomFunction] for a free-text search over fields:
// name("text") expands into
//
//	field1.matches(re) || field2.matches(re) || ...
//
// where re is "(?i)" followed by the trimmed text quoted with
// [regexp.QuoteMeta], so the search is case-insensitive and literal (no
// wildcards, no anchors) and every translator check — allowlist, field
// mapping, regex validation — applies to the expansion unchanged. It
// combines with the rest of a filter like any predicate:
//
//	filterparser.WithCustomFunctions(map[string]filter.CustomFunction{
//	    "search": functions.TextSearch([]string{"code", "name", "description"}),
//	})
//	// search("network") && lawCode == "44fz"
//
// The argument must be one non-blank string literal; anything else fails
// with [filter.ErrInvalidExpression]. Field names are API names (before
// [filter.WithFieldMapping]). The returned function reports ErrInvalidOptions when no fields are configured.
//
// No full-text index is used: on MongoDB each field becomes an unanchored
// case-insensitive $regex, which scans. Translators escape what their engine
// cannot carry (the MongoDB translator spells a NUL byte \x00).
func TextSearch(fields []string, opts ...Option) filter.CustomFunction {
	o := newOptions(opts...)
	fields = slices.Clone(fields)

	return func(args []filter.Node) (filter.Node, error) {
		if len(fields) == 0 || !o.operator.IsStringOp() {
			return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "text search needs at least one field")
		}

		if len(args) != 1 {
			return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "text search takes one string argument")
		}

		lit, ok := args[0].(*filter.LiteralNode)
		if !ok || lit == nil {
			return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "text search takes a string literal")
		}

		text, ok := lit.Value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "text search takes a non-empty string")
		}

		pattern := strings.TrimSpace(text)
		if o.operator == filter.OpMatches {
			pattern = regexp.QuoteMeta(pattern)
			if o.caseInsensitive {
				pattern = "(?i)" + pattern
			}
		}

		var out filter.Node

		for _, f := range fields {
			m := &filter.CallNode{Op: o.operator, Target: &filter.IdentNode{Name: f}, Args: []filter.Node{&filter.LiteralNode{Value: pattern}}}
			if out == nil {
				out = m
				continue
			}

			out = &filter.BinaryOpNode{Op: filter.OpOr, Left: out, Right: m}
		}

		return out, nil
	}
}
