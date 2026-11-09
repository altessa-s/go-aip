// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"strings"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// MatchAll is the clause emitted for a nil AST — a filter that selects
// every row. Returning a valid predicate rather than an empty string
// keeps `"... WHERE " + clause` concatenation safe at the call site.
const MatchAll = "1 = 1"

// MatchNone is the clause emitted for `field in []`. CEL evaluates
// membership in an empty list to false, and every SQL dialect here
// rejects the literal `IN ()` as a syntax error.
const MatchNone = "1 = 0"

// FilterTranslator walks a filter AST and renders it as a SQL WHERE clause,
// deferring every backend-specific decision to a [FilterDialect].
//
// Two output modes share one walk. [FilterTranslator.Translate] emits bind
// placeholders and collects the values into a separate argument slice;
// [FilterTranslator.TranslateInline] emits self-contained SQL with every
// literal rendered in place.
//
// A FilterTranslator is safe for concurrent Translate and TranslateInline calls.
type FilterTranslator struct {
	telemetry *filter.Telemetry
	config    *filter.Policy
	dialect   FilterDialect
	encode    func(any) (any, error)
	render    PredicateRenderer
	args      []any
	depth     filter.DepthGuard
	inline    bool
	// qualifies is set for a [QualifiedNamesDialect]: dotted fields must be declared by the policy.
	qualifies bool
	// inSubstring marks the IN list of a guarded substring() operand.
	inSubstring bool
	// negated marks a predicate under an odd number of `!` operators. Negation is pushed down to the predicates rather
	// than rendered as NOT around a clause, see [FilterTranslator.visitUnaryOp].
	negated bool
}

// NewFilterTranslator builds a translator for the given dialect over a policy already bound
// to its storage names by [filter.NewTranslatorContext]. It returns
// [filter.ErrPolicyRequired] for a nil policy. A decimal field declaration
// needs a [DecimalDialect] or [WithFilterLiteralEncoder]; otherwise it fails with
// [filter.ErrUnsupportedType] rather than comparing through a float.
func NewFilterTranslator(policy *filter.Policy, dialect FilterDialect, opts ...FilterOption) (*FilterTranslator, error) {
	if policy == nil {
		return nil, filter.ErrPolicyRequired
	}

	if nilcheck.IsNil(dialect) {
		return nil, filter.ErrInvalidOptions
	}

	o := newFilterOptions(opts...)

	name := unnamedDialect
	if named, ok := dialect.(NamedFilterDialect); ok && named.Name() != "" {
		name = named.Name()
	}

	t := &FilterTranslator{config: policy, dialect: dialect, encode: o.literalEncoder, render: o.predicateRenderer,
		depth: filter.NewDepthGuard(policy.MaxDepth()), telemetry: filter.NewTelemetry(o.collector, name)}

	if q, ok := dialect.(QualifiedNamesDialect); ok {
		t.qualifies = q.QualifiesNames()
	}

	if !t.decimalSupported() {
		if err := policy.RejectFieldKinds(filter.FieldKindDecimal); err != nil {
			return nil, err
		}
	}

	return t, nil
}

// Translate converts a filter AST node to a parameterized WHERE clause.
// Every literal from the expression is replaced by a bind placeholder
// and returned in args, positionally aligned with its placeholder — no
// value from the filter reaches the SQL text, so the clause cannot be
// made to carry an injection regardless of the input.
//
// A nil node translates to [MatchAll] and an empty argument list.
func (t *FilterTranslator) Translate(node filter.Node) (string, []any, error) {
	done := t.telemetry.Start()
	defer done()

	state := *t
	sql, err := state.run(node, false)
	args := state.args

	if err != nil {
		return "", nil, err
	}

	return sql, args, nil
}

// TranslateInline converts a filter AST node to a self-contained WHERE
// clause with every literal rendered in place. Use it where a
// parameterized query is not an option — view definitions, generated
// DDL, logging, debugging.
//
// Prefer [FilterTranslator.Translate] for anything driven by request data:
// inline rendering puts caller-controlled values into the SQL text, and
// its safety rests entirely on the dialect's quoting rather than on the
// protocol-level separation the placeholder form gives for free.
func (t *FilterTranslator) TranslateInline(node filter.Node) (string, error) {
	done := t.telemetry.Start()
	defer done()

	state := *t
	sql, err := state.run(node, true)

	return sql, err
}

// run resets the per-call state and walks the AST in the requested mode.
func (t *FilterTranslator) run(node filter.Node, inline bool) (string, error) {
	t.depth.Reset()
	t.inline = inline
	t.negated = false

	t.args = nil
	if t.render != nil {
		if err := t.config.CheckFields(node); err != nil {
			return "", err
		}
	}

	if err := t.config.CheckLimits(node); err != nil {
		return "", err
	}

	if node == nil {
		return MatchAll, nil
	}

	return t.acceptPredicate(node)
}

// visitLiteral validates a literal value and returns it unchanged. The
// rendering decision — placeholder or inline text — belongs to the
// operand that consumes the value, so it is deferred to [FilterTranslator.value].
func (t *FilterTranslator) visitLiteral(n *filter.LiteralNode) (any, error) {
	if t.encode == nil && n.Value != nil && !supportedValue(n.Value) {
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedType, "%T", n.Value)
	}

	return n.Value, nil
}

// visitIdent converts an identifier to a quoted column reference. The
// name is checked against the allow-list and run through the field
// mapping before the dialect validates and quotes it, so a mapped name
// can never break out of its quotes.
//
// Under a [QualifiedNamesDialect], a dotted name must be declared by the policy: the dot becomes a table qualifier, so
// a name the client composed under a wildcard could reach a column of another joined table.
func (t *FilterTranslator) visitIdent(n *filter.IdentNode) (any, error) {
	if !t.config.IsFieldAllowed(n.Name) {
		return nil, coreerrs.Wrapf(filter.ErrFieldNotAllowed, "%s", n.Name)
	}

	if t.qualifies && strings.Contains(n.Name, ".") && !t.config.DeclaresField(n.Name) {
		return nil, coreerrs.Wrapf(filter.ErrFieldNotAllowed,
			"%s: a dotted field names a table qualifier and must be declared (WithAllowedFields, WithFieldTypes or WithFieldMapping)",
			n.Name)
	}

	return t.dialect.QuoteIdent(t.config.ApplyFieldMapping(n.Name))
}

// visitBinaryOp converts a binary operation to a SQL clause.
func (t *FilterTranslator) visitBinaryOp(n *filter.BinaryOpNode) (any, error) {
	if err := t.depth.Enter(); err != nil {
		return nil, err
	}
	defer t.depth.Leave()

	switch n.Op {
	case filter.OpAnd:
		return t.translateLogical("AND", n.Left, n.Right)
	case filter.OpOr:
		return t.translateLogical("OR", n.Left, n.Right)
	case filter.OpIn:
		return t.translateIn(n.Left, n.Right)
	default:
		return t.translateComparison(n.Op, n.Left, n.Right)
	}
}

// visitUnaryOp converts a unary operation to a SQL clause.
//
// `!` is pushed down to the predicates (De Morgan over && and ||) instead of rendered as NOT around its operand, because
// SQL's NOT keeps an unknown (NULL) result unknown, while the evaluator's `!` holds whenever its operand is false: a
// NULL or absent field makes `x == v`, `x in [...]` and the string predicates false, so their negation matches it.
// Each predicate renders its own exact negation instead: `!(x == v)` is `x != v`, which holds for NULL, and an ordered
// comparison, which the evaluator finds unknown for NULL, stays `NOT (x < v)`, which never matches NULL.
func (t *FilterTranslator) visitUnaryOp(n *filter.UnaryOpNode) (any, error) {
	if err := t.depth.Enter(); err != nil {
		return nil, err
	}
	defer t.depth.Leave()

	if n.Op == filter.OpNot {
		t.negated = !t.negated
		inner, err := t.acceptPredicate(n.Operand)
		t.negated = !t.negated

		if err != nil {
			return nil, err
		}

		return inner, nil
	}

	return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "unary operator %v", n.Op)
}

// visitCall converts a function call to a SQL clause.
func (t *FilterTranslator) visitCall(n *filter.CallNode) (any, error) {
	if err := t.depth.Enter(); err != nil {
		return nil, err
	}
	defer t.depth.Leave()

	switch n.Op {
	case filter.OpContains, filter.OpStartsWith, filter.OpEndsWith, filter.OpMatches:
		return t.translateStringPredicate(n)
	case filter.OpHas, filter.OpExists:
		return t.translateExists(n.Target)
	case filter.OpSize:
		// A length expression is an integer, not a predicate. It is
		// rendered by operandSQL when it appears inside a comparison; on
		// its own there is nothing for WHERE to test.
		return nil, coreerrs.Wrap(filter.ErrUnsupportedOperation, "size() outside a comparison")
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

	return filter.VisitElements(visitor{t}, n)
}

// visitor adapts a [FilterTranslator] to [filter.Visitor] for the walk. The visit methods stay unexported: they operate on
// the per-call state of one Translate or TranslateInline run, so a caller invoking them directly on a shared translator
// would race with concurrent translations and bypass the field, limit and telemetry checks of a run. A visitor holds a
// single pointer, so converting it to the interface does not allocate.
type visitor struct{ t *FilterTranslator }

var _ filter.Visitor = visitor{}

func (v visitor) VisitLiteral(n *filter.LiteralNode) (any, error)   { return v.t.visitLiteral(n) }
func (v visitor) VisitIdent(n *filter.IdentNode) (any, error)       { return v.t.visitIdent(n) }
func (v visitor) VisitBinaryOp(n *filter.BinaryOpNode) (any, error) { return v.t.visitBinaryOp(n) }
func (v visitor) VisitUnaryOp(n *filter.UnaryOpNode) (any, error)   { return v.t.visitUnaryOp(n) }
func (v visitor) VisitCall(n *filter.CallNode) (any, error)         { return v.t.visitCall(n) }
func (v visitor) VisitList(n *filter.ListNode) (any, error)         { return v.t.visitList(n) }
