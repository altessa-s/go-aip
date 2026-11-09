// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package clickhouse renders go-aip requests for ClickHouse: filter expressions (AIP-160) as WHERE clauses, order specs
// (AIP-132) as ORDER BY bodies and read masks (AIP-157) as SELECT column lists, and serves keyset List pages.
//
// Every constructor takes the resource's shared, client-facing policy, built once in API names and reused by every backend,
// followed by storage options that bind it to ClickHouse column names. A nil policy fails with the policy package's
// ErrPolicyRequired.
//
// Filter: NewFilterTranslator takes a *filter.Policy and FilterOption values. WithFilterOptions binds the policy to storage
// names (filter.WithFieldMapping); WithFilterCollector, WithFilterLiteralEncoder, WithFilterPredicateRenderer and
// WithFilterTimestampPrecision configure metrics, value encoding, trusted predicate overrides and inline DateTime64
// precision. FilterTranslator.Translate emits `?` placeholders with bound arguments; TranslateInline renders literals in
// place. filter.NewPolicy requires an allowlist or an explicit trusted WithAnyField opt-out.
//
// Order and projection: NewOrderTranslator and NewProjectionTranslator are sqlbase.NewOrderTranslator and
// sqlbase.NewProjectionTranslator with the ClickHouse dialect fixed; OrderOption and ProjectionOption are aliases of the
// sqlbase option types (sqlbase.WithOrderOptions, sqlbase.WithProjectionOptions and the collectors). The
// projection translator renders an exclusion as `* EXCEPT (col, …)`.
//
// Schema filter: NewSchema binds a neutral *sqlbase.Schema to SchemaOption values: WithSchemaContractMode (required),
// WithSchemaDecimalFormat, WithSchemaTimestampPrecision and WithSchemaMaxDataBudget. NewFilterTranslatorWithSchema takes
// the policy, that Schema and the shared WithFilterOptions and WithFilterCollector (the scalar options fail with
// filter.ErrInvalidOptions) and renders the listing storage contract with typed operands: Scalar columns are NOT NULL and store their zero value,
// PresenceScalar columns are Nullable, Array columns are native Array(T) without NULL elements, and JSONPath fields
// read a JSON String column through JSONExtract guarded by JSONType (a missing or null value, or one of another JSON
// type, is NULL). Decimal operands are cast to WithSchemaDecimalFormat, timestamps to DateTime64 at
// WithSchemaTimestampPrecision; NULL and negation follow the default evaluator, as in the PostgreSQL and MariaDB
// contract translators. What ClickHouse cannot express (whole JSON values, UUID columns, decimal or bytes JSON paths)
// fails with filter.ErrUnsupportedOperation.
//
// Listing: NewListEngine is sqlbase.NewListEngine with ListDialect fixed: backtick identifiers, `?` placeholders, typed
// cursor operands, system.columns schema checks, minmax skipping indexes and the schema filter translator.
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
//	trans, err := clickhouse.NewFilterTranslator(policy)
//	if err != nil {
//	    return err
//	}
//	where, args, err := trans.Translate(ast)
//	// where: (`status` = ?) AND (`type` IN (?, ?))
//	// args:  []any{int64(2), int64(1), int64(2)}
//
//	order, err := clickhouse.NewOrderTranslator(orderPolicy,
//	    sqlbase.WithOrderOptions(orderby.WithFieldMapping(map[string]string{"createTime": "created_at"})))
//	body, err := order.Translate(spec) // `created_at` DESC, `name` ASC
//
//	proj, err := clickhouse.NewProjectionTranslator(projectionPolicy)
//	cols, err := proj.Translate(mask) // "" for every column, or * EXCEPT (...)
//
//	list, err := clickhouse.NewListEngine[Widget](db, signer, widgetSchema, "widgets", []string{"id", "name"},
//	    func(w *Widget) []any { return []any{&w.ID, &w.Name} }, sqlbase.WithListScopeKeys("tenant_id"))
//	page, err := list.List(ctx, "widgets.List", req, sqlbase.Scope{"tenant_id": tenant})
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package clickhouse
