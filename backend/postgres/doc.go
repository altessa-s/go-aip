// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package postgres renders and evaluates go-aip requests for PostgreSQL: filter expressions (AIP-160) as WHERE clauses,
// order specs (AIP-132) as ORDER BY bodies, read masks (AIP-157) as SELECT column lists, and keyset list pages over a
// PostgreSQL table.
//
// Every translator constructor takes the resource's shared, client-facing policy, built once in API names and reused by
// every backend, followed by storage options that bind it to PostgreSQL column names. A nil policy fails with the policy
// package's ErrPolicyRequired.
//
// Filter: NewFilterTranslator takes a *filter.Policy and FilterOption values. WithFilterOptions binds the policy to storage
// names (filter.WithFieldMapping); WithFilterCollector, WithFilterLiteralEncoder and WithFilterPredicateRenderer configure
// metrics, value encoding and trusted predicate overrides. FilterTranslator.Translate emits numbered $n placeholders with
// bound arguments; TranslateInline renders literals in place. NULL follows the default evaluator's semantics.
// filter.NewPolicy requires an allowlist or an explicit trusted WithAnyField opt-out.
//
// Schema: NewSchema wraps a neutral sqlbase.Schema with PostgreSQL types (exact NUMERIC, arrays, JSONB, bytea, timestamps)
// and SchemaOption values. A Schema compiles a filter into a Program that renders SQL and evaluates rows in memory with the
// same NULL semantics; it is the filter/evaluator profile (evaluator.WithProfile) and the input of
// NewFilterTranslatorWithSchema, so SQL and in-memory matching agree. WithSchemaContractMode selects the listing storage
// contract; the schema translator takes only the shared WithFilterOptions and WithFilterCollector, and the scalar options
// fail with filter.ErrInvalidOptions.
//
// Order, projection and listing: NewOrderTranslator, NewProjectionTranslator and NewListEngine are the sqlbase
// constructors with the PostgreSQL dialect fixed. OrderOption, ProjectionOption and ListEngineOption alias the sqlbase
// option types (sqlbase.WithOrderOptions, sqlbase.WithProjectionOptions, sqlbase.WithList…). ListDialect is the
// PostgreSQL sqlbase.ListDialect used by NewListEngine; embed it to override selected listing behavior.
//
// # Usage
//
//	p, _ := filterparser.New()
//	ast, _ := p.Parse(ctx, `status == 2 && type in [1, 2]`)
//
//	policy, err := filter.NewPolicy(filter.WithAllowedFields("status", "type"))
//	if err != nil {
//	    return err
//	}
//	trans, err := postgres.NewFilterTranslator(policy)
//	if err != nil {
//	    return err
//	}
//	where, args, err := trans.Translate(ast)
//	// where: ("status" = $1) AND ("type" IN ($2, $3))
//	// args:  []any{int64(2), int64(1), int64(2)}
//
//	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{"amount": sqlbase.Scalar("amount", sqlbase.Numeric)})
//	schema, err := postgres.NewSchema(fields)
//	typed, err := postgres.NewFilterTranslatorWithSchema(policy, schema)
//	eval, err := evaluator.New(policy, evaluator.WithProfile(schema), evaluator.WithValueDecoder(sqlbase.Decoder{}))
//
//	order, err := postgres.NewOrderTranslator(orderPolicy,
//	    sqlbase.WithOrderOptions(orderby.WithFieldMapping(map[string]string{"createTime": "created_at"})))
//	body, err := order.Translate(spec) // "created_at" DESC, "name" ASC
//
//	list, err := postgres.NewListEngine[Widget](db, codec, widgetSchema, "widgets", []string{"id", "name"},
//	    func(w *Widget) []any { return []any{&w.ID, &w.Name} }, sqlbase.WithListScopeKeys("tenant_id"))
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package postgres
