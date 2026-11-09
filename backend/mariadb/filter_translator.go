// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb

import (
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// FilterTranslator converts filter AST nodes to MariaDB SQL WHERE clauses.
//
// Translate emits `?` placeholders and returns the bound values
// alongside the clause; TranslateInline emits self-contained SQL with
// every literal rendered in place. Both run the shared SQL walker —
// see [sqlbase.FilterTranslator].
//
// A FilterTranslator is safe for concurrent Translate and TranslateInline calls.
type FilterTranslator struct {
	base *sqlbase.FilterTranslator
}

// SchemaFilterTranslator renders schema-aware MariaDB predicates under the listing storage contract of a [Schema]
// created with [WithSchemaContractMode]. It is safe for concurrent calls.
type SchemaFilterTranslator struct {
	telemetry *filter.Telemetry
	schema    *Schema
	policy    *filter.Policy
}

// NewFilterTranslatorWithSchema creates a schema-aware listing contract translator. The schema carries every storage
// setting; opts take only [WithFilterOptions] and [WithFilterCollector], and the options of the scalar translator fail
// with [filter.ErrInvalidOptions] instead of being ignored. A nil schema, or one without [WithSchemaContractMode],
// returns filter.ErrInvalidOptions; use NewFilterTranslator for the original MariaDB semantics.
func NewFilterTranslatorWithSchema(policy *filter.Policy, schema *Schema, opts ...FilterOption) (*SchemaFilterTranslator, error) {
	o := newFilterOptions(opts...)
	if o.scalarOnly() {
		return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "schema translation uses schema value encoding and rendering")
	}

	if !schema.ContractMode() {
		return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "MariaDB schema translation requires contract mode")
	}

	policy, err := filter.NewTranslatorContext(policy, o.filterOptions...)
	if err != nil {
		return nil, err
	}

	if err = schema.ValidatePolicy(policy); err != nil {
		return nil, err
	}

	return &SchemaFilterTranslator{schema: schema, policy: policy, telemetry: filter.NewTelemetry(o.collector, dialectName)}, nil
}

// Translate validates the complete filter tree before rendering MariaDB SQL.
// Each invocation owns its argument buffer and is safe for concurrent use.
func (t *SchemaFilterTranslator) Translate(node filter.Node) (string, []any, error) {
	done := t.telemetry.Start()
	defer done()

	root, err := t.validate(node)
	if err != nil {
		return "", nil, err
	}

	if root == nil {
		// A nil node matches every row.
		return "1 = 1", nil, nil
	}

	r := contractRenderer{policy: t.policy, options: &t.schema.opts, digits: t.schema.digits, names: make(map[string]bool)}
	r.reserve(root)

	sql, err := r.predicate(root)
	if err != nil {
		return "", nil, err
	}

	return sql, r.args, nil
}

// NewFilterTranslator creates a new MariaDB translator over policy with the given
// options. Returns [filter.ErrPolicyRequired] for a nil
// policy. The policy holds the field rules in CEL-side names and may be
// shared with other translators; [WithFilterOptions] maps them to storage
// names. [WithFilterDecimalFormat] sets the DECIMAL domain decimal operands
// are cast to.
func NewFilterTranslator(policy *filter.Policy, opts ...FilterOption) (*FilterTranslator, error) {
	o := newFilterOptions(opts...)

	policy, err := filter.NewTranslatorContext(policy, o.filterOptions...)
	if err != nil {
		return nil, err
	}

	if err = o.decimalFormat.Validate(); err != nil {
		return nil, err
	}

	native := dialect{customRegex: !policy.UsesRE2Regex(), decimalFormat: o.decimalFormat}

	base, err := sqlbase.NewFilterTranslator(policy, native, sqlbase.WithFilterLiteralEncoder(o.literalEncoder),
		sqlbase.WithFilterPredicateRenderer(o.predicateRenderer), sqlbase.WithFilterCollector(o.collector))
	if err != nil {
		return nil, err
	}

	return &FilterTranslator{base: base}, nil
}

// Translate converts n to a parameterized WHERE clause and its bound values, using isolated state, and records
// translation metrics ([sqlbase.FilterTranslator.Translate]).
func (t *FilterTranslator) Translate(n filter.Node) (string, []any, error) {
	return t.base.Translate(n)
}

// TranslateInline converts n to a self-contained WHERE clause with every literal rendered in place, using isolated state,
// and records translation metrics ([sqlbase.FilterTranslator.TranslateInline]).
func (t *FilterTranslator) TranslateInline(n filter.Node) (string, error) {
	return t.base.TranslateInline(n)
}
