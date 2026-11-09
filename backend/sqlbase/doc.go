// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package sqlbase holds the SQL machinery shared by the PostgreSQL, MariaDB and ClickHouse backends: the filter predicate
// builder, the neutral SQL field schema, the database/sql value decoder, the dialect-generic order and projection
// translators, the keyset listing engine and the dialect kit.
//
// Filter: FilterTranslator shares policy validation, AST traversal, operation budgets, argument collection, and inline/bound
// rendering across SQL dialects; a FilterDialect owns identifiers, placeholders, literals, sizes, and string predicates.
// ValueFunc must be used once for each SQL occurrence of a bound value.
//
// Schema: NewSchema captures public names mapped to immutable Field descriptions. Scalar, PresenceScalar, Array, and JSONPath
// describe storage without selecting a database, driver, evaluator, or SQL renderer.
//
// Decoder handles database/sql nullable values and driver.Valuer without importing a concrete driver; pass it with
// evaluator.WithValueDecoder and a compiled SQL profile.
//
// Order and projection: NewOrderTranslator and NewProjectionTranslator take the resource's shared policy and an OrderDialect or
// ProjectionDialect (postgres.OrderDialect, mariadb.OrderDialect, clickhouse.OrderDialect and their projection counterparts).
// TranslateOrder, NewProjectionContext, TranslateProjection and TranslateProjectionView render through an application
// ColumnResolver instead, for trusted storage expressions. An attribute a dialect cannot express is rejected, never dropped.
//
// Listing: NewListEngine pages a SQL table with keyset pagination through a ListDialect. The PostgreSQL, MariaDB and
// ClickHouse dialects live in their backend packages (postgres.ListDialect, mariadb.ListDialect, clickhouse.ListDialect);
// sqlbase holds no database-specific code, only the database-neutral building blocks a dialect composes: DecodeCursor,
// ExpandKeysetPredicate, CheckListColumns and the ListSchemaConfig helpers. A dialect may also implement
// ListSortKeyDialect to select sort keys through an expression.
//
// Dialect kit: every port a SQL dialect implements is public, so a dialect for another database can be built outside this
// repository like the built-in ones. Style quotes table names, derives index names and binds placeholders in a dialect's
// form; Identifiers is the column-reference grammar of its order and projection dialects; IsPortableIdent, Unqualified and
// Qualifier are the shared name helpers. The subpackage sqltype types a schema-aware filter for its renderer, and
// sqlbasetest fakes the database/sql driver for its list engine tests.
//
// # Usage
//
//	bound, err := filter.NewTranslatorContext(policy, filter.WithFieldMapping(columns))
//	if err != nil { return err }
//	translator, err := sqlbase.NewFilterTranslator(bound, dialect)
//	if err != nil { return err }
//	where, args, err := translator.Translate(node)
//
//	order, err := sqlbase.NewOrderTranslator(orderPolicy, postgres.OrderDialect(),
//	    sqlbase.WithOrderOptions(orderby.WithFieldMapping(map[string]string{"create_time": "created_at"})))
//	clause, err := order.Translate(spec) // `"created_at" DESC, "slug" ASC`
//
//	list, err := sqlbase.NewListEngine[Widget](db, codec, widgetSchema, "widgets", columns,
//		func(w *Widget) []any { return []any{&w.ID, &w.Name} }, dialect,
//		sqlbase.WithListScopeKeys("tenant_id"), sqlbase.WithListParentKey("tenant_id"))
//	page, err := list.List(ctx, "widgets.List", req, sqlbase.Scope{"tenant_id": req.Parent})
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package sqlbase
