// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"strings"

	"github.com/altessa-s/go-aip/filter"
)

// renderIn renders `left in right`. A null left operand (a NULL column, the null literal) is in a list constructor holding a
// null literal or a NULL column, as in the default evaluator; with an array value it is handled as by [noElement].
//
//nolint:gocognit,gocyclo // one case per operand shape
func (r *renderer) renderIn(e *expression, negated bool) (string, error) {
	left, right := e.Children[0], e.Children[1]
	if right.Tag == filter.NodeKindList && len(right.Children) == 0 {
		if !negated {
			return sqlFalse, nil
		}

		if !computed(left) {
			return sqlTrue, nil
		}

		a, err := r.render(left)
		if err != nil {
			return "", err
		}

		return "((" + a + ") IS NOT NULL)", nil
	}

	a, err := r.render(left)
	if err != nil {
		return "", err
	}

	c, err := r.render(right)
	if err != nil {
		return "", err
	}

	if r.contract && left.Array && right.Tag == filter.NodeKindList {
		sql := "(" + a + " && " + c + ")"
		if negated {
			return "(NOT COALESCE(" + sql + ", FALSE))", nil
		}

		return sql, nil
	}

	member := "(" + a + " = ANY(" + c + "))"

	if right.Tag != filter.NodeKindList {
		if !negated {
			return member, nil
		}

		return r.noElement(member, left, a, c), nil
	}

	var (
		nullLiteral        bool
		nullColumns, maybe []string
	)

	for _, leaf := range listLeaves(right, nil) {
		switch {
		case leaf.IsNull():
			nullLiteral = true
		case nullable(leaf, r.contract), computed(leaf):
			v, err := r.render(leaf)
			if err != nil {
				return "", err
			}

			if computed(leaf) {
				maybe = append(maybe, v)
			} else {
				nullColumns = append(nullColumns, v)
			}
		}
	}

	safeLeft := nullSafe(left, r.contract)

	if !negated {
		parts := []string{member}

		switch {
		case !safeLeft:
		case nullLiteral:
			parts = append(parts, "("+a+") IS NULL")
		default:
			for _, col := range nullColumns {
				parts = append(parts, "(("+a+") IS NULL AND ("+col+") IS NULL)")
			}
		}

		if len(parts) == 1 {
			return member, nil
		}

		return "(" + strings.Join(parts, " OR ") + ")", nil
	}

	if !nullLiteral && len(nullColumns) == 0 && len(maybe) == 0 {
		if safeLeft {
			return "(NOT " + member + " OR (" + a + ") IS NULL)", nil
		}

		return "(NOT " + member + ")", nil
	}

	parts := []string{"NOT COALESCE(" + member + ", FALSE)"}

	switch {
	case computed(left):
		parts = append(parts, "("+a+") IS NOT NULL")
	case !safeLeft:
	case nullLiteral:
		parts = append(parts, "("+a+") IS NOT NULL")
	default:
		for _, col := range nullColumns {
			parts = append(parts, "(("+a+") IS NOT NULL OR ("+col+") IS NOT NULL)")
		}
	}

	for _, v := range maybe {
		parts = append(parts, "("+v+") IS NOT NULL")
	}

	return "(" + strings.Join(parts, " AND ") + ")", nil
}
