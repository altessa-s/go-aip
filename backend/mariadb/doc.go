// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package mariadb renders AIP request clauses for MariaDB (and MySQL): filters, order keys, read-mask projections and
// keyset-paginated listing.
//
// Each kind has its own constructor:
//
//   - [NewFilterTranslator] converts a filter AST to a WHERE body with `?` placeholders ([FilterTranslator.Translate])
//     or inline literals ([FilterTranslator.TranslateInline]). It takes the shared *filter.Policy (field
//     authorization, types, limits, decimal conversion, value codecs and regex rules, in API names, built once per
//     resource) and [FilterOption] values; [WithFilterOptions] binds the policy to storage names
//     (filter.WithFieldMapping). A nil policy fails with filter.ErrPolicyRequired.
//   - [NewFilterTranslatorWithSchema] builds the listing contract translator over a [Schema]: [NewSchema] binds a
//     sqlbase.Schema to [SchemaOption] values (contract mode, which it requires, literal bounds, timestamp precision,
//     text collation, array aliases and decimal domain). It takes only the shared [WithFilterOptions] and
//     [WithFilterCollector]; the scalar options fail with filter.ErrInvalidOptions.
//   - [NewOrderTranslator] and [NewProjectionTranslator] are the sqlbase translators with the MariaDB dialect fixed.
//   - [NewListEngine] is the sqlbase listing engine with [ListDialect] fixed.
//
// Use the generic sqlbase constructors directly for an application sqlbase.ColumnResolver or a customized
// [ListDialect].
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
//	trans, err := mariadb.NewFilterTranslator(policy)
//	if err != nil {
//	    return err
//	}
//	where, args, err := trans.Translate(ast)
//	// where: (`status` = ?) AND (`type` IN (?, ?))
//	// args:  []any{int64(2), int64(1), int64(2)}
//
//	order, err := mariadb.NewOrderTranslator(orderPolicy)
//	sort, err := order.Translate(spec) // `created_at` DESC, `id` ASC
//
//	rows, err := db.QueryContext(ctx, "SELECT * FROM events WHERE "+where+" ORDER BY "+sort, args...)
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package mariadb
