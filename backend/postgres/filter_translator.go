// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// FilterTranslator converts filter AST nodes to PostgreSQL WHERE clauses.
//
// Translate emits numbered `$n` placeholders and returns the bound
// values alongside the clause; TranslateInline emits self-contained SQL
// with every literal rendered in place. Both run the shared SQL
// walker — see [sqlbase.FilterTranslator].
//
// A FilterTranslator is safe for concurrent Translate and TranslateInline calls.
type FilterTranslator struct {
	base *sqlbase.FilterTranslator
}

// NewFilterTranslator creates a new PostgreSQL translator over policy with the
// given options. Returns [filter.ErrPolicyRequired] for a nil
// policy. The policy holds the field rules in CEL-side names and may be
// shared with other translators; [WithFilterOptions] maps them to storage
// names.
func NewFilterTranslator(policy *filter.Policy, opts ...FilterOption) (*FilterTranslator, error) {
	o := newFilterOptions(opts...)

	bound, err := filter.NewTranslatorContext(policy, o.filterOptions...)
	if err != nil {
		return nil, err
	}

	base, err := sqlbase.NewFilterTranslator(bound, filterDialect{}, sqlbase.WithFilterLiteralEncoder(o.literalEncoder),
		sqlbase.WithFilterPredicateRenderer(o.predicateRenderer), sqlbase.WithFilterCollector(o.collector))
	if err != nil {
		return nil, err
	}

	return &FilterTranslator{base: base}, nil
}

// SchemaFilterTranslator renders schema-aware PostgreSQL predicates. It is immutable
// and safe for concurrent calls; each call builds its own expression program.
type SchemaFilterTranslator struct {
	telemetry *filter.Telemetry
	schema    *Schema
	policy    *filter.Policy
}

// NewFilterTranslatorWithSchema enables PostgreSQL-native numeric, array and JSONB
// operations. Use the same immutable schema with evaluator.WithProfile. The schema
// carries every storage setting, the contract mode ([WithSchemaContractMode]) included;
// opts take only [WithFilterOptions] and [WithFilterCollector], and the options of the
// scalar translator fail with [filter.ErrInvalidOptions] instead of being ignored.
func NewFilterTranslatorWithSchema(policy *filter.Policy, schema *Schema, opts ...FilterOption) (*SchemaFilterTranslator, error) {
	if schema == nil {
		return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "nil PostgreSQL schema")
	}

	o := newFilterOptions(opts...)
	if o.scalarOnly() {
		return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "schema translation uses schema value encoding and rendering")
	}

	policy, err := filter.NewTranslatorContext(policy, o.filterOptions...)
	if err != nil {
		return nil, err
	}

	if err = schema.ValidatePolicy(policy); err != nil {
		return nil, err
	}

	return &SchemaFilterTranslator{telemetry: filter.NewTelemetry(o.collector, dialectName), schema: schema, policy: policy}, nil
}

// Translate renders a schema-aware expression with numbered placeholders.
func (t *SchemaFilterTranslator) Translate(node filter.Node) (string, []any, error) {
	done := t.telemetry.Start()
	defer done()

	var (
		program *Program
		err     error
	)
	if t.schema.ContractMode() {
		program, err = t.schema.CompileContract(t.policy, node)
	} else {
		program, err = t.schema.Compile(t.policy, node)
	}

	if err != nil {
		return "", nil, err
	}

	if t.schema.ContractMode() {
		return program.SQLContract()
	}

	return program.SQL()
}

// TranslateInline renders the same schema-aware expression with inline literals.
func (t *SchemaFilterTranslator) TranslateInline(node filter.Node) (string, error) {
	done := t.telemetry.Start()
	defer done()

	var (
		program *Program
		err     error
	)
	if t.schema.ContractMode() {
		program, err = t.schema.CompileContract(t.policy, node)
	} else {
		program, err = t.schema.Compile(t.policy, node)
	}

	if err != nil {
		return "", err
	}

	if t.schema.ContractMode() {
		return program.InlineContract()
	}

	return program.Inline()
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
