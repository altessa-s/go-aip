// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis

import (
	"fmt"

	"github.com/altessa-s/go-aip/filter"

	coremaps "github.com/altessa-s/go-atlas-core/collections/maps"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// FilterTranslator converts filter AST nodes to RediSearch query strings.
//
// Every storage name (after the field mapping) is written as `@name` with
// every ASCII character but a letter, a digit and `_` backslash-escaped, so a
// dotted name such as "address.city" is one attribute (`@address\.city`). A
// name must be valid UTF-8 without white space or control characters, and
// its dot-separated segments must not be empty; any other name fails with
// [filter.ErrInvalidExpression], whatever the policy allows.
type FilterTranslator struct {
	predicateRenderer func(filter.Node) (string, bool, error)
	strictSchema      bool
	telemetry         *filter.Telemetry
	literalEncoder    func(any) (any, error)
	config            *filter.Policy
	schema            *coremaps.ImmutableMap[string, FieldType]
	indexMissing      map[string]struct{}
	indexEmpty        map[string]struct{}
	depth             filter.DepthGuard
}

// NewFilterTranslator creates a new RediSearch translator over an index whose fields are all TAG and none is declared
// INDEXMISSING or INDEXEMPTY; [NewFilterTranslatorWithSchema] describes another index. Returns [filter.ErrPolicyRequired]
// for a nil policy. The policy holds the field rules in CEL-side names and may be shared with other translators;
// [WithFilterOptions] maps them to storage names.
func NewFilterTranslator(policy *filter.Policy, opts ...FilterOption) (*FilterTranslator, error) {
	return NewFilterTranslatorWithSchema(policy, defaultSchema(), opts...)
}

// NewFilterTranslatorWithSchema creates a RediSearch translator for the index schema describes ([NewSchema]): each storage
// field (after the field mapping) renders in the syntax of its index type, and null, presence and empty-string tests need
// the field declared INDEXMISSING or INDEXEMPTY. Returns [filter.ErrPolicyRequired] for a nil policy and
// [filter.ErrInvalidOptions] for a nil schema.
func NewFilterTranslatorWithSchema(policy *filter.Policy, schema *Schema, opts ...FilterOption) (*FilterTranslator, error) {
	o := newFilterOptions(opts...)

	ctx, err := filter.NewTranslatorContext(policy, o.filterOptions...)
	if err != nil {
		return nil, err
	}

	if schema == nil {
		return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "nil RediSearch schema")
	}
	// Decimal literals cannot be bound exactly here yet: refuse the declaration rather than compare through a float.
	if err = ctx.RejectFieldKinds(filter.FieldKindDecimal); err != nil && o.literalEncoder == nil {
		return nil, err
	}

	return &FilterTranslator{strictSchema: schema.strict, literalEncoder: o.literalEncoder, predicateRenderer: o.predicateRenderer,
		telemetry: filter.NewTelemetry(o.collector, "redis"), config: ctx, schema: schema.fields,
		indexMissing: schema.indexMissing, indexEmpty: schema.indexEmpty, depth: filter.NewDepthGuard(ctx.MaxDepth())}, nil
}

// Translate converts a filter AST node to a RediSearch query string.
// Returns "*" for a nil node (match all).
func (t *FilterTranslator) Translate(node filter.Node) (string, error) {
	done := t.telemetry.Start()
	defer done()

	state := *t

	return state.translate(node)
}

func (t *FilterTranslator) translate(node filter.Node) (string, error) {
	// Authorization before shape: every field reference is vetted against the allow-list, together with the limits, before any
	// operand shape check can report a denied field as an unsupported operation.
	if err := t.config.CheckFields(node); err != nil {
		return "", err
	}

	if t.strictSchema {
		for n := range filter.AllNodes(node) {
			if id, ok := n.(*filter.IdentNode); ok {
				if _, found := t.schema.Get(t.config.ApplyFieldMapping(id.Name)); !found {
					return "", coreerrs.Wrapf(filter.ErrFieldNotAllowed, "unknown schema field %s", id.Name)
				}
			}
		}
	}

	if node == nil {
		return "*", nil
	}

	t.depth.Reset()

	s, err := t.acceptPredicate(node)
	if err != nil {
		return "", err
	}

	if s == "" {
		return "*", nil
	}

	return s, nil
}

// acceptPredicate visits a node expected to produce a query fragment.
//
// A bare identifier is treated as a boolean TAG test (`@field:{true}`),
// matching the CEL semantics of using a field directly as a condition.
// The negated form has always been handled in translateNot; this is its
// counterpart, so `active` and `!active` are now symmetric. Without it a
// bare identifier reached the server as a free-text term and silently
// matched whatever the TEXT fields happened to contain.
//
// The TAG form is hardcoded rather than resolved through the schema, for
// the same reason translateNot hardcodes it: a boolean is only ever
// indexed as a TAG, and a NUMERIC or TEXT rendering of `true` would not
// mean anything.
func (t *FilterTranslator) acceptPredicate(node filter.Node) (string, error) {
	if t.predicateRenderer != nil {
		if out, handled, err := t.predicateRenderer(node); handled || err != nil {
			return out, err
		}
	}

	if ident, ok := node.(*filter.IdentNode); ok {
		field, err := t.getFieldName(ident)
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("%s:{true}", queryField(field)), nil
	}

	// A literal is a value, never a predicate. visitLiteral formats the value
	// on its own, so a bare string literal would be handed to RediSearch as a
	// query fragment — free-text terms, field selectors and all — instead of
	// being compared against a field the allow-list vetted.
	if lit, ok := node.(*filter.LiteralNode); ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "expected filter clause, got literal %T", lit.Value)
	}

	return t.getQueryString(node)
}

// visitLiteral converts a literal value to its string representation.
func (t *FilterTranslator) visitLiteral(n *filter.LiteralNode) (any, error) {
	return t.formatLiteral(n.Value)
}

// visitIdent converts an identifier to a field reference: the storage name,
// which the query renders as `@` and the name with every ASCII character but
// a letter, a digit and `_` backslash-escaped. A storage name that is not an
// attribute name — see [FilterTranslator] — fails with
// [filter.ErrInvalidExpression] even when the policy allows the field.
func (t *FilterTranslator) visitIdent(n *filter.IdentNode) (any, error) {
	field := n.Name
	if !t.config.IsFieldAllowed(field) {
		return nil, coreerrs.Wrapf(filter.ErrFieldNotAllowed, "%s", field)
	}

	name := t.config.ApplyFieldMapping(field)
	if !validAttribute(name) {
		return nil, coreerrs.Wrapf(filter.ErrInvalidExpression, "%q is not a valid RediSearch attribute", name)
	}

	return name, nil
}

// visitBinaryOp converts a binary operation to a RediSearch query fragment.
func (t *FilterTranslator) visitBinaryOp(n *filter.BinaryOpNode) (any, error) {
	if err := t.depth.Enter(); err != nil {
		return nil, err
	}
	defer t.depth.Leave()

	switch n.Op {
	case filter.OpAnd:
		return t.translateLogicalAnd(n.Left, n.Right)
	case filter.OpOr:
		return t.translateLogicalOr(n.Left, n.Right)
	case filter.OpIn:
		return t.translateIn(n.Left, n.Right)
	default:
		return t.translateComparison(n.Op, n.Left, n.Right)
	}
}

// visitUnaryOp converts a unary operation to a RediSearch query fragment.
func (t *FilterTranslator) visitUnaryOp(n *filter.UnaryOpNode) (any, error) {
	if err := t.depth.Enter(); err != nil {
		return nil, err
	}
	defer t.depth.Leave()

	if n.Op == filter.OpNot {
		return t.translateNot(n.Operand)
	}

	return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "unary operator %v", n.Op)
}

// visitCall converts a function call to a RediSearch query fragment.
func (t *FilterTranslator) visitCall(n *filter.CallNode) (any, error) {
	if err := t.depth.Enter(); err != nil {
		return nil, err
	}
	defer t.depth.Leave()

	switch n.Op {
	case filter.OpContains:
		return t.translateContains(n.Target, n.Args)
	case filter.OpStartsWith:
		return t.translateStartsWith(n.Target, n.Args)
	case filter.OpEndsWith:
		return t.translateEndsWith(n.Target, n.Args)
	case filter.OpMatches:
		return nil, coreerrs.Wrap(filter.ErrUnsupportedOperation, "matches is not supported by RediSearch")
	case filter.OpSize:
		return nil, coreerrs.Wrap(filter.ErrUnsupportedOperation, "size is not supported by RediSearch")
	case filter.OpHas, filter.OpExists:
		return t.translateExists(n.Target)
	default:
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "function %v", n.Op)
	}
}

// visitList converts a list to a slice of values.
func (t *FilterTranslator) visitList(n *filter.ListNode) (any, error) {
	if err := t.depth.Enter(); err != nil {
		return nil, err
	}
	defer t.depth.Leave()

	return filter.VisitLiteralElements(visitor{t}, n)
}

// visitor adapts a [FilterTranslator] to [filter.Visitor] for the walk. The visit methods stay unexported: they operate on the
// per-call state of one Translate run, so a caller invoking them directly on a shared translator would race with concurrent
// translations and bypass the field, limit and telemetry checks of a run. A visitor holds a single pointer, so converting it to
// the interface does not allocate.
type visitor struct{ t *FilterTranslator }

var _ filter.Visitor = visitor{}

func (v visitor) VisitLiteral(n *filter.LiteralNode) (any, error)   { return v.t.visitLiteral(n) }
func (v visitor) VisitIdent(n *filter.IdentNode) (any, error)       { return v.t.visitIdent(n) }
func (v visitor) VisitBinaryOp(n *filter.BinaryOpNode) (any, error) { return v.t.visitBinaryOp(n) }
func (v visitor) VisitUnaryOp(n *filter.UnaryOpNode) (any, error)   { return v.t.visitUnaryOp(n) }
func (v visitor) VisitCall(n *filter.CallNode) (any, error)         { return v.t.visitCall(n) }
func (v visitor) VisitList(n *filter.ListNode) (any, error)         { return v.t.visitList(n) }
