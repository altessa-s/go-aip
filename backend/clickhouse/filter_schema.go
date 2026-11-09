// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse

import (
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// SchemaFilterTranslator renders schema-aware ClickHouse predicates under the listing storage contract: typed
// operands from a [Schema] (Scalar columns are NOT NULL and store their zero value, PresenceScalar columns are
// Nullable, Array columns are native Array(T) without NULL elements, JSONPath fields are read from a JSON String
// column with JSONExtract). Create it with [NewFilterTranslatorWithSchema]. It is safe for concurrent calls.
type SchemaFilterTranslator struct {
	telemetry *filter.Telemetry
	schema    *Schema
	policy    *filter.Policy
}

// NewFilterTranslatorWithSchema creates a schema-aware listing contract translator over policy. The schema carries
// every storage setting and needs [WithSchemaContractMode]: without it, or with a nil schema, it returns
// [filter.ErrInvalidOptions]; use [NewFilterTranslator] for the policy-driven ClickHouse semantics. opts take only
// [WithFilterOptions] and [WithFilterCollector]; the options of the scalar translator fail with
// [filter.ErrInvalidOptions] instead of being ignored. [WithFilterOptions] maps the policy's names to storage names
// and must agree with the schema's columns ([Schema.ValidatePolicy]).
func NewFilterTranslatorWithSchema(policy *filter.Policy, schema *Schema, opts ...FilterOption) (*SchemaFilterTranslator, error) {
	o := newFilterOptions(opts...)
	if o.scalarOnly() {
		return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "schema translation uses schema value encoding and rendering")
	}

	if !schema.ContractMode() {
		return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "ClickHouse schema translation requires contract mode")
	}

	bound, err := filter.NewTranslatorContext(policy, o.filterOptions...)
	if err != nil {
		return nil, err
	}

	if err = schema.ValidatePolicy(bound); err != nil {
		return nil, err
	}

	return &SchemaFilterTranslator{schema: schema, policy: bound, telemetry: filter.NewTelemetry(o.collector, dialectName)}, nil
}

// Translate validates the complete filter tree before rendering ClickHouse SQL with `?` placeholders and the bound
// arguments. A nil node renders [sqlbase.MatchAll]. Each invocation owns its argument buffer and is safe for
// concurrent use.
func (t *SchemaFilterTranslator) Translate(node filter.Node) (string, []any, error) {
	done := t.telemetry.Start()
	defer done()

	root, err := t.validate(node)
	if err != nil {
		return "", nil, err
	}

	if root == nil {
		return sqlbase.MatchAll, nil, nil
	}

	r := schemaRenderer{policy: t.policy, options: &t.schema.opts, digits: t.schema.digits}

	sql, err := r.predicate(root)
	if err != nil {
		return "", nil, err
	}

	return sql, r.args, nil
}

// ValidateAST checks compatibility with this translator's schema, policy and encoding.
// It renders and discards the result without contacting the backend.
func (t *SchemaFilterTranslator) ValidateAST(n filter.Node) error {
	_, _, err := t.Translate(n)
	return err
}
