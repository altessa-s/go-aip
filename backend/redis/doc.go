// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package redis renders the Redis Query Engine (RediSearch) form of every go-aip request DSL: the FT.SEARCH query string of a
// filter, its SORTBY instruction and its RETURN clause. It renders arguments only and never contacts Redis.
//
// Each translator binds the resource's shared, client-facing policy to the storage options of this backend, and every option is
// prefixed with its kind (WithFilter…, WithSchema…, WithOrder…, WithProjection…):
//
//   - FilterTranslator (NewFilterTranslator, NewFilterTranslatorWithSchema, FilterOption) converts a filter AST into a
//     RediSearch query string. A Schema (NewSchema, SchemaOption) describes the FT.CREATE index: the RediSearch type
//     (FieldType) of each storage field — undeclared fields are TAG unless WithSchemaStrict rejects them — and, through
//     WithSchemaIndexMissing and WithSchemaIndexEmpty, the fields declared INDEXMISSING and INDEXEMPTY, which alone admit null,
//     presence and empty-string tests. NewFilterTranslator uses an empty schema (every field TAG). WithFilterOptions binds the
//     filter.Policy to storage names (filter.WithFieldMapping). A nil policy fails with filter.ErrPolicyRequired. A storage name is written
//     as `@name` with every ASCII character but a letter, a digit and `_` backslash-escaped (`@address\.city`); a name that is
//     not valid UTF-8, holds white space or control characters or an empty dot-separated segment fails with
//     filter.ErrInvalidExpression.
//   - OrderTranslator (NewOrderTranslator, OrderOption) converts an orderby.Spec into a single-field SortBy, since
//     FT.SEARCH ... SORTBY accepts one key; more keys fail with orderby.ErrTooManySortKeys. WithOrderOptions carries the storage
//     options (mapping, KeyResolver, denied storage fields, tie-breakers).
//   - ProjectionTranslator (NewProjectionTranslator, ProjectionOption) converts a projection.Spec into the arguments
//     RETURN n field …, RETURN 0 for a ModeNone selection, or nil when every field is returned. RETURN has no exclusion form, so a
//     policy whose authorized fields would exclude denied fields is rejected at construction with
//     projection.ErrAllFieldsUnresolvable.
//
// WithFilterCollector, WithOrderCollector and WithProjectionCollector record translation durations. Translators are safe for
// concurrent use after construction.
//
// # Usage
//
//	filterPolicy, _ := filter.NewPolicy(filter.WithAllowedFields("status", "priority"))
//	schema, err := redis.NewSchema(map[string]redis.FieldType{
//	    "status":   redis.FieldTypeNumeric,
//	    "priority": redis.FieldTypeNumeric,
//	})
//	if err != nil {
//	    return err
//	}
//	filters, err := redis.NewFilterTranslatorWithSchema(filterPolicy, schema)
//	if err != nil {
//	    return err
//	}
//	p, _ := filterparser.New()
//	ast, _ := p.Parse(ctx, `status == 1 && priority >= 3`)
//	query, _ := filters.Translate(ast)
//	// query == "(@status:[1 1] @priority:[3 +inf])"
//
//	orderPolicy, _ := orderby.NewPolicy(orderby.WithAllowedFields("createdAt"))
//	orders, _ := redis.NewOrderTranslator(orderPolicy)
//	sortBy, _ := orders.Translate(ob)
//	// sortBy == redis.SortBy{Field: "createdAt", Descending: true} for "createdAt desc"
//
//	projectionPolicy, _ := projection.NewPolicy(projection.WithAllowedFields("name", "email"))
//	projections, _ := redis.NewProjectionTranslator(projectionPolicy,
//	    redis.WithProjectionOptions(projection.WithRequiredFields("id")),
//	)
//	args, _ := projections.Translate(spec)
//	// args == []string{"RETURN", "2", "id", "name"} for the read mask "name"
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package redis
