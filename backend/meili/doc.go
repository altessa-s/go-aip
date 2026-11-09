// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package meili renders filter, order and projection requests as the parameters of a Meilisearch search: the filter
// expression (FilterTranslator), the sort argument (OrderTranslator) and attributesToRetrieve (ProjectionTranslator).
//
// Each constructor takes the resource's shared, client-facing policy (*filter.Policy, *orderby.Policy,
// *projection.Policy, built once per resource in API names) and the options of its kind. Every option is prefixed with
// its kind: WithFilter…, WithOrder…, WithProjection…; WithFilterOptions, WithOrderOptions and WithProjectionOptions
// carry the core storage options (field mapping and friends). A nil policy fails with the DSL's ErrPolicyRequired.
// Translators are safe for concurrent use after construction.
//
// # Usage
//
//	fp, _ := filter.NewPolicy(filter.WithAllowedFields("status", "type"))
//	ft, err := meili.NewFilterTranslator(fp, meili.WithFilterServerVersion(meili.Version{Major: 1, Minor: 12}))
//	if err != nil {
//	    return err
//	}
//	expr, _ := ft.Translate(ast) // (status = 2) AND (type IN [1, 2])
//
//	op, _ := orderby.NewPolicy(orderby.WithAllowedFields("createdAt", "name"))
//	ot, _ := meili.NewOrderTranslator(op)
//	sort, _ := ot.Translate(ob) // []string{"createdAt:desc", "name:asc"}
//
//	pp, _ := projection.NewPolicy(projection.WithAllowedFields("title", "author.name"))
//	pt, _ := meili.NewProjectionTranslator(pp, meili.WithProjectionOptions(projection.WithRequiredFields("id")))
//	attrs, _ := pt.Translate(spec) // []string{"id", "title"} for the selection "title"
//
//	req := &meilisearch.SearchRequest{Filter: expr, Sort: sort, AttributesToRetrieve: attrs}
//
// # Filter
//
// An element test on an array field (`"red" in tags`, with filter.WithArrayFields) renders as `tags = "red"`; null,
// empty-string and size() emptiness tests render with IS NULL / IS EMPTY / EXISTS. WithFilterServerVersion declares the
// Meilisearch release, so an operator that release lacks fails with filter.ErrUnsupportedOperation;
// WithFilterContainsFilter declares the experimental containsFilter feature, which contains() needs.
//
// The evaluator's three-valued logic is kept under negation: ! is pushed down through && and || to each predicate, and a
// negated ordering or bare boolean renders its opposite (`!(a < v)` is `a >= v`, `!active` is `active = false`), which,
// like the evaluator's unknown, leaves a null or missing attribute unmatched; Meilisearch's NOT would select it.
//
// Attributes are written unquoted, so every storage name must be dot-separated, non-empty segments of ASCII letters,
// digits, `_` and `-`, and the filter translator also refuses the words the filter grammar reserves; any other name fails
// (filter.ErrInvalidExpression, orderby.ErrInvalidFieldPath, projection.ErrInvalidFieldPath) whatever the policy allows.
// String values are double-quoted with `\` and `"` escaped; before Meilisearch 1.4, which unescapes only `\"`, a backslash is
// written as it is.
//
// # Order
//
// Meilisearch sorts null and missing attributes last in both directions: nulls last is accepted, nulls first and
// collations fail with orderby.ErrUnsupportedOrdering. An empty order translates to nil, so the sort parameter can be
// omitted.
//
// # Projection
//
// attributesToRetrieve has no exclusion form: a policy whose every authorized field would exclude denied fields is
// rejected at construction with projection.ErrAllFieldsUnresolvable, and a ModeNone selection without required fields
// with projection.ErrUnsupportedSelection. Translate returns nil when every attribute is returned.
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package meili
