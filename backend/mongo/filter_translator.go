// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"time"

	"github.com/shopspring/decimal"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/filter"

	metrics "github.com/altessa-s/go-aip/metrics"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Query and aggregation operators the translator emits in several places.
const (
	opAnd    = "$and"
	opEq     = "$eq"
	opNe     = "$ne"
	opExists = "$exists"
	opExpr   = "$expr"
	opSize   = "$size"
	opGt     = "$gt"
	opGte    = "$gte"
	opLt     = "$lt"
	opLte    = "$lte"
	opNor    = "$nor"
)

// orderingOps maps the ordering operators to their MongoDB spelling, the same in a query and in an aggregation expression.
var orderingOps = map[filter.Operator]string{filter.OpGT: opGt, filter.OpGTE: opGte, filter.OpLT: opLt, filter.OpLTE: opLte}

// FilterTranslator converts filter AST nodes to MongoDB bson.M filters.
//
// A FilterTranslator is safe for concurrent Translate calls: each call walks the filter on its own copy of the traversal
// state.
type FilterTranslator struct {
	predicateRenderer func(filter.Node) (bson.M, bool, error)
	telemetry         *metrics.TranslateTimer
	literalEncoder    func(any) (any, error)
	config            *filter.Policy
	semantics         *Schema
	depth             filter.DepthGuard
}

// NewFilterTranslator creates a new MongoDB translator over policy with the given
// options. Returns [filter.ErrPolicyRequired] for a nil
// policy. The policy holds the field rules in CEL-side names and may be
// shared with other translators; [WithFilterOptions] maps them to storage
// names.
func NewFilterTranslator(policy *filter.Policy, opts ...FilterOption) (*FilterTranslator, error) {
	schema, err := NewSchema()
	if err != nil {
		return nil, err
	}

	return NewFilterTranslatorWithSchema(policy, schema, opts...)
}

// NewFilterTranslatorWithSchema creates a MongoDB translator with explicit storage
// semantics over the shared filter policy.
func NewFilterTranslatorWithSchema(policy *filter.Policy, schema *Schema, opts ...FilterOption) (*FilterTranslator, error) {
	if schema == nil {
		return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "nil MongoDB schema")
	}

	o := newFilterOptions(opts...)

	ctx, err := filter.NewTranslatorContext(policy, o.filterOptions...)
	if err != nil {
		return nil, err
	}

	bound, err := schema.BindPolicy(ctx)
	if err != nil {
		return nil, err
	}

	return &FilterTranslator{predicateRenderer: o.predicateRenderer, telemetry: filter.NewTelemetry(o.collector, "mongo"),
		literalEncoder: o.literalEncoder, config: ctx, semantics: bound, depth: filter.NewDepthGuard(ctx.MaxDepth())}, nil
}

// Translate converts a filter AST node to a MongoDB bson.M filter.
//
// A nil node translates to an empty filter document, which MongoDB reads
// as match-all. Returning that rather than dereferencing the nil lets a
// caller pass an absent filter straight through — the same contract the
// SQL and RediSearch translators offer.
func (t *FilterTranslator) Translate(node filter.Node) (bson.M, error) {
	done := t.telemetry.Start()
	defer done()

	state := *t

	return state.translate(node, nil)
}

// translateRewritten is [FilterTranslator.TranslateD] of rewrite(node), for a caller that rewrites a filter into a form of the
// same meaning first: the limits and the allow-list are checked on node, the filter as the client wrote it, so the nodes the
// rewrite adds do not count against the operation budget — as for the translator's own negation normal form. rewrite must
// return a fresh tree that names no field node does not and nests no deeper; it is not called for a nil node.
func (t *FilterTranslator) translateRewritten(node filter.Node, rewrite func(filter.Node) filter.Node) (bson.D, error) {
	done := t.telemetry.Start()
	defer done()

	state := *t

	m, err := state.translate(node, rewrite)
	if err != nil {
		return nil, err
	}

	return orderedDocument(m), nil
}

func (t *FilterTranslator) translate(node filter.Node, rewrite func(filter.Node) filter.Node) (bson.M, error) {
	// Authorization before shape: every field reference is vetted against the allow-list, together with the limits, before any
	// operand shape check can report a denied field as an unsupported operation.
	if err := t.config.CheckFields(node); err != nil {
		return nil, err
	}

	if node == nil {
		return bson.M{}, nil
	}

	t.depth.Reset()

	if rewrite != nil {
		node = rewrite(node)
	}

	if t.hasPresenceFields() {
		node = filter.NegationNormalForm(node, t.config.ArrayFields(), t.semantics.PresenceFields())
	}

	return t.acceptPredicate(node)
}

func (t *FilterTranslator) hasPresenceFields() bool {
	// Presence fields are declared through the Mongo storage schema.
	return t.semantics != nil && t.semantics.HasPresenceFields()
}

// TranslateD is [FilterTranslator.Translate] with ordered output: every document of the filter is a bson.D, its keys in sorted order,
// recursively — so translating the same AST twice yields the same BSON bytes. That is what a fingerprint of the filter needs, such
// as the token binding of store/mongo FindPage, which marshals the filter it executes once and binds the token to those bytes.
//
// Sorting never changes what the filter matches: the translator emits no embedded-document literal (literals are scalars,
// timestamps, bytes and Decimal128 values), so every document it builds is a query or operator document, whose key order MongoDB
// ignores. A nil node yields an empty bson.D.
func (t *FilterTranslator) TranslateD(node filter.Node) (bson.D, error) {
	m, err := t.Translate(node)
	if err != nil {
		return nil, err
	}

	return orderedDocument(m), nil
}

// acceptPredicate visits a node expected to produce a filter document.
//
// A bare identifier is treated as a boolean field test ({field: true}),
// matching the CEL semantics of using a field directly as a condition.
// The negated form has always been handled in translateNot; this is its
// counterpart, so `active` and `!active` are now symmetric wherever a
// predicate is expected — at the root and on either side of && / ||.
func (t *FilterTranslator) acceptPredicate(node filter.Node) (bson.M, error) {
	if t.predicateRenderer != nil {
		if out, handled, err := t.predicateRenderer(node); handled || err != nil {
			return out, err
		}
	}

	if ident, ok := node.(*filter.IdentNode); ok {
		field, err := t.getFieldName(ident)
		if err != nil {
			return nil, err
		}

		return bson.M{field: true}, nil
	}

	result, err := node.Accept(visitor{t})
	if err != nil {
		return nil, err
	}

	m, ok := result.(bson.M)
	if !ok {
		return nil, coreerrs.Wrapf(filter.ErrInvalidExpression, "expected bson.M, got %T", result)
	}

	return m, nil
}

// visitLiteral converts a literal value to its BSON representation.
func (t *FilterTranslator) visitLiteral(n *filter.LiteralNode) (any, error) {
	return t.convertValue(n.Value)
}

// visitIdent converts an identifier to a field reference.
func (t *FilterTranslator) visitIdent(n *filter.IdentNode) (any, error) {
	field := n.Name
	if !t.config.IsFieldAllowed(field) {
		return nil, coreerrs.Wrapf(filter.ErrFieldNotAllowed, "%s", field)
	}

	return t.config.ApplyFieldMapping(field), nil
}

// visitBinaryOp converts a binary operation to a MongoDB filter.
func (t *FilterTranslator) visitBinaryOp(n *filter.BinaryOpNode) (any, error) {
	if err := t.depth.Enter(); err != nil {
		return nil, err
	}
	defer t.depth.Leave()

	switch n.Op {
	case filter.OpAnd:
		return t.translateLogical(opAnd, n.Left, n.Right)
	case filter.OpOr:
		return t.translateLogical("$or", n.Left, n.Right)
	case filter.OpIn:
		return t.translateIn(n.Left, n.Right)
	default:
		return t.translateComparison(n.Op, n.Left, n.Right)
	}
}

// visitUnaryOp converts a unary operation to a MongoDB filter.
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

// visitCall converts a function call to a MongoDB filter.
func (t *FilterTranslator) visitCall(n *filter.CallNode) (any, error) {
	if err := t.depth.Enter(); err != nil {
		return nil, err
	}
	defer t.depth.Leave()

	// A field argument has no regex form: it is compared under $expr.
	if n.Op == filter.OpContains || n.Op == filter.OpStartsWith || n.Op == filter.OpEndsWith {
		if len(n.Args) == 1 {
			if _, isLit := n.Args[0].(*filter.LiteralNode); !isLit {
				return t.translateStringExpr(n.Op, n.Target, n.Args[0])
			}
		}
	}

	switch n.Op {
	case filter.OpContains:
		return t.translateRegexOp(n.Target, n.Args, regexContains, false)
	case filter.OpStartsWith:
		return t.translateRegexOp(n.Target, n.Args, regexStartsWith, false)
	case filter.OpEndsWith:
		return t.translateRegexOp(n.Target, n.Args, regexEndsWith, false)
	case filter.OpMatches:
		return t.translateRegexOp(n.Target, n.Args, t.regexPassthrough, true)
	case filter.OpSize:
		return nil, translateSize(n.Target)
	case filter.OpHas, filter.OpExists:
		return t.translateHas(n.Target)
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

// fieldLabel returns the CEL-side name of a field operand, for a field error.
func fieldLabel(node filter.Node) string {
	if ident, ok := node.(*filter.IdentNode); ok {
		return ident.Name
	}

	return ""
}

// convertValue converts Go values to MongoDB-compatible values.
func (t *FilterTranslator) convertValue(v any) (any, error) {
	if t.literalEncoder != nil {
		var err error

		v, err = t.literalEncoder(v)
		if err != nil {
			return nil, err
		}
	}

	switch val := v.(type) {
	case nil, bool, int64, uint64, float64, string, []byte, time.Time, decimal.Decimal:
		return val, nil
	default:
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedType, "%T", v)
	}
}
