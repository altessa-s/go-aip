// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/altessa-s/go-aip/filter"

	metrics "github.com/altessa-s/go-aip/metrics"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// FilterTranslator converts filter AST nodes to Meilisearch filter expressions.
//
// Attributes are written unquoted, so every storage name (after the field
// mapping) must be one or more dot-separated segments of ASCII letters,
// digits, `_` and `-`, and must not be a word the filter grammar reserves
// (AND, OR, IN, NOT, TO, EXISTS, IS, NULL, EMPTY, CONTAINS, STARTS, WITH) or a
// geo or foreign-filter function name. Any other name fails with
// [filter.ErrInvalidExpression], whatever the policy allows.
type FilterTranslator struct {
	predicateRenderer func(filter.Node) (string, bool, error)
	telemetry         *metrics.TranslateTimer
	literalEncoder    func(any) (any, error)
	config            *filter.Policy
	depth             filter.DepthGuard
	features          features
}

// NewFilterTranslator creates a new Meilisearch translator over policy with the
// given options. Returns [filter.ErrPolicyRequired] for a nil
// policy. The policy holds the field rules in CEL-side names and may be
// shared with other translators; [WithFilterOptions] maps them to storage
// names.
func NewFilterTranslator(policy *filter.Policy, opts ...FilterOption) (*FilterTranslator, error) {
	o := newFilterOptions(opts...)

	ctx, err := filter.NewTranslatorContext(policy, o.filterOptions...)
	if err != nil {
		return nil, err
	}
	// Decimal literals cannot be bound exactly here yet: refuse the declaration rather than compare through a float.
	if err = ctx.RejectFieldKinds(filter.FieldKindDecimal); err != nil && o.literalEncoder == nil {
		return nil, err
	}

	feats, err := newFeatures(o.serverVersion, o.containsFilter)
	if err != nil {
		return nil, err
	}

	return &FilterTranslator{literalEncoder: o.literalEncoder, predicateRenderer: o.predicateRenderer, features: feats,
		telemetry: filter.NewTelemetry(o.collector, "meili"), config: ctx, depth: filter.NewDepthGuard(ctx.MaxDepth())}, nil
}

// Translate converts a filter AST node to a Meilisearch filter expression.
//
// It goes through acceptString so that a bare identifier at the root of
// an expression becomes a boolean test (`field = true`), the same as one
// appearing inside && or ||. Visiting the node directly would emit the
// bare attribute name, which Meilisearch rejects as a missing operator.
// A nil node translates to the empty filter, which Meilisearch reads as
// no filtering at all. Returning that rather than dereferencing the nil
// lets a caller pass an absent filter straight through — the same
// contract the SQL and RediSearch translators offer.
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

	if node == nil {
		return "", nil
	}

	t.depth.Reset()

	return t.acceptString(node)
}

// visitLiteral converts a literal value to its Meilisearch representation.
func (t *FilterTranslator) visitLiteral(n *filter.LiteralNode) (any, error) {
	if t.literalEncoder != nil {
		return n.Value, nil
	}

	switch n.Value.(type) {
	case nil, bool, int64, uint64, float64, string, []byte, time.Time:
		return n.Value, nil
	default:
		return nil, coreerrs.Wrapf(filter.ErrUnsupportedType, "%T", n.Value)
	}
}

// visitIdent converts an identifier to a field reference. The storage name is
// written unquoted, so one that is not a plain attribute name — see
// [FilterTranslator] — fails with [filter.ErrInvalidExpression] even when the
// policy allows the field.
func (t *FilterTranslator) visitIdent(n *filter.IdentNode) (any, error) {
	field := n.Name
	if !t.config.IsFieldAllowed(field) {
		return nil, coreerrs.Wrapf(filter.ErrFieldNotAllowed, "%s", field)
	}

	name := t.config.ApplyFieldMapping(field)
	if !validFilterAttribute(name) {
		return nil, coreerrs.Wrapf(filter.ErrInvalidExpression, "%q is not a valid Meilisearch filter attribute", name)
	}

	return name, nil
}

// visitBinaryOp converts a binary operation to a Meilisearch filter.
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

// visitUnaryOp converts a unary operation to a Meilisearch filter.
func (t *FilterTranslator) visitUnaryOp(n *filter.UnaryOpNode) (any, error) {
	if err := t.depth.Enter(); err != nil {
		return nil, err
	}
	defer t.depth.Leave()

	if n.Op == filter.OpNot {
		return t.negate(n.Operand)
	}

	return nil, coreerrs.Wrapf(filter.ErrUnsupportedOperation, "unary operator %v", n.Op)
}

// visitCall converts a function call to a Meilisearch filter.
func (t *FilterTranslator) visitCall(n *filter.CallNode) (any, error) {
	if err := t.depth.Enter(); err != nil {
		return nil, err
	}
	defer t.depth.Leave()

	switch n.Op {
	case filter.OpContains:
		if !t.features.contains {
			return nil, coreerrs.Wrap(filter.ErrUnsupportedOperation,
				"contains() needs the experimental containsFilter feature (Meilisearch 1.10 or later, WithFilterContainsFilter)")
		}

		return t.translateStringFunc(n.Target, n.Args, "CONTAINS")
	case filter.OpStartsWith:
		if !t.features.startsWith {
			return nil, coreerrs.Wrap(filter.ErrUnsupportedOperation,
				"startsWith() needs Meilisearch 1.17 or later, or 1.11 or later with the containsFilter feature")
		}

		return t.translateStringFunc(n.Target, n.Args, "STARTS WITH")
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

// translateComparison handles comparison operators.
func (t *FilterTranslator) translateComparison(op filter.Operator, left, right filter.Node) (string, error) {
	if isSizeCall(left) || isSizeCall(right) {
		return t.translateSizeComparison(op, left, right, false)
	}

	// Type-check first: a mirrored `"lit" == field` is a type mismatch, not
	// a malformed field reference.
	if err := t.config.CheckComparison(left, right); err != nil {
		return "", err
	}

	field, err := t.getFieldName(left)
	if err != nil {
		return "", err
	}

	right, err = t.config.NormalizeDecimalOperand(left, right)
	if err != nil {
		return "", err
	}

	value, err := filter.AcceptLiteral(visitor{t}, right)
	if err != nil {
		return "", err
	}

	return t.buildComparisonFilter(field, t.isArrayField(left), op, value)
}

// buildComparisonFilter creates a Meilisearch comparison filter.
func (t *FilterTranslator) buildComparisonFilter(field string, array bool, op filter.Operator, value any) (string, error) {
	value, err := t.encode(value)
	if err != nil {
		return "", err
	}

	if value == nil {
		// CEL's null is one question — "this field has no value" — and
		// Meilisearch splits it into two: the attribute may be absent
		// from the document, or present and null. IS NULL alone answers
		// only the second, so `deletedAt == null` would miss every
		// document that simply omits the attribute, and `!= null` would
		// match all of them. The second is the dangerous half: it is the
		// shape of a soft-delete filter, and it would return the deleted
		// documents too. Both halves are covered explicitly.
		switch op {
		case filter.OpEqual:
			return t.nullPredicate(field)
		case filter.OpNotEqual:
			if !t.features.nullEmpty {
				return "", errNeedsNullEmpty("comparison with null")
			}

			return "(" + field + " EXISTS AND " + field + " IS NOT NULL)", nil
		default:
			return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "comparison %v with null", op)
		}
	}

	if isEmptyString(value) && (op == filter.OpEqual || op == filter.OpNotEqual) {
		// Meilisearch does not index the empty string as a filter value, so `field = ""` matches nothing and
		// `field != ""` everything; IS EMPTY is its spelling of the empty value.
		pred, emptyErr := t.emptyStringPredicate(field, array)
		if emptyErr != nil || op == filter.OpEqual {
			return pred, emptyErr
		}

		return "NOT (" + pred + ")", nil
	}

	formatted, err := t.formatLiteral(value)
	if err != nil {
		return "", err
	}

	var meiliOp string

	switch op {
	case filter.OpEqual:
		meiliOp = "="
	case filter.OpNotEqual:
		meiliOp = "!="
	case filter.OpLT:
		meiliOp = "<"
	case filter.OpLTE:
		meiliOp = "<="
	case filter.OpGT:
		meiliOp = ">"
	case filter.OpGTE:
		meiliOp = ">="
	default:
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "comparison operator %v", op)
	}

	return field + " " + meiliOp + " " + formatted, nil
}

// translateLogical handles && and || operators.
func (t *FilterTranslator) translateLogical(meiliOp string, left, right filter.Node) (string, error) {
	return t.joinLogical(meiliOp, left, right, t.acceptString)
}

// joinLogical renders `(left) meiliOp (right)`, each operand rendered by render.
func (t *FilterTranslator) joinLogical(meiliOp string, left, right filter.Node, render func(filter.Node) (string, error)) (string, error) {
	leftStr, err := render(left)
	if err != nil {
		return "", err
	}

	rightStr, err := render(right)
	if err != nil {
		return "", err
	}

	return "(" + leftStr + ") " + meiliOp + " (" + rightStr + ")", nil
}

// translateIn handles the in operator.
func (t *FilterTranslator) translateIn(left, right filter.Node) (string, error) {
	if filter.IsFieldReference(right) {
		return t.translateMembership(left, right)
	}

	field, err := t.getFieldName(left)
	if err != nil {
		return "", err
	}

	if ident, ok := left.(*filter.IdentNode); ok {
		if err = t.config.CheckLiteralKind(ident.Name, right); err != nil {
			return "", err
		}
	}

	right, err = t.config.NormalizeDecimalOperand(left, right)
	if err != nil {
		return "", err
	}

	values, err := right.Accept(visitor{t})
	if err != nil {
		return "", err
	}

	valuesSlice, ok := values.([]any)
	if !ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "in operator requires a list, got %T", values)
	}

	return t.buildInFilter(field, t.isArrayField(left), valuesSlice)
}

// buildInFilter renders `field IN [values]`. A null or empty-string element has no IN spelling on Meilisearch — the
// filter values it indexes include neither — so it becomes its own predicate, ORed with the IN of the other elements.
func (t *FilterTranslator) buildInFilter(field string, array bool, values []any) (string, error) {
	formatted := make([]string, 0, len(values))

	var withNull, withEmpty bool

	for _, raw := range values {
		v, err := t.encode(raw)
		if err != nil {
			return "", err
		}

		switch {
		case v == nil:
			withNull = true
		case isEmptyString(v):
			withEmpty = true
		default:
			s, err := t.formatLiteral(v)
			if err != nil {
				return "", err
			}

			formatted = append(formatted, s)
		}
	}

	var parts []string
	if len(formatted) > 0 || !withNull && !withEmpty {
		parts = append(parts, field+" IN ["+strings.Join(formatted, ", ")+"]")
	}

	if withNull {
		if array {
			return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "null element in an in list of array field %s", field)
		}

		pred, err := t.nullPredicate(field)
		if err != nil {
			return "", err
		}

		parts = append(parts, pred)
	}

	if withEmpty {
		pred, err := t.emptyStringPredicate(field, array)
		if err != nil {
			return "", err
		}

		parts = append(parts, pred)
	}

	if len(parts) == 1 {
		return parts[0], nil
	}

	return "(" + strings.Join(parts, " OR ") + ")", nil
}

// translateMembership handles `value in field`. On an array field ([filter.WithArrayFields]) it is the element test
// Meilisearch spells as `field = value`: equality on an array matches a document when any element equals the value.
func (t *FilterTranslator) translateMembership(left, right filter.Node) (string, error) {
	if _, err := t.getFieldName(right); err != nil {
		return "", err
	}

	lit, ok := left.(*filter.LiteralNode)
	if !ok {
		return "", coreerrs.Wrapf(filter.ErrUnsupportedOperation, "in operator with a %T element and a field operand", left)
	}

	if !t.isArrayField(right) {
		return "", coreerrs.Wrap(filter.ErrInvalidExpression,
			"in operator requires a list or an array field declared with filter.WithArrayFields")
	}

	if lit.Value == nil {
		return "", coreerrs.Wrap(filter.ErrUnsupportedOperation, "null membership in an array field")
	}

	return t.translateComparison(filter.OpEqual, right, left)
}

// translateStringFunc handles single-argument string predicates: contains, startsWith.
func (t *FilterTranslator) translateStringFunc(target filter.Node, args []filter.Node, meiliOp string) (string, error) {
	field, err := t.getFieldName(target)
	if err != nil {
		return "", err
	}

	if len(args) != 1 {
		return "", coreerrs.Wrap(filter.ErrInvalidExpression, "string function requires exactly 1 argument")
	}

	arg, err := filter.AcceptLiteral(visitor{t}, args[0])
	if err != nil {
		return "", err
	}

	pattern, ok := arg.(string)
	if !ok {
		return "", coreerrs.Wrap(filter.ErrInvalidExpression, "string function argument must be a string")
	}

	quoted, err := t.quote(pattern)
	if err != nil {
		return "", err
	}

	return field + " " + meiliOp + " " + quoted, nil
}

// translateExists handles the has() / exists function.
func (t *FilterTranslator) translateExists(target filter.Node) (string, error) {
	field, err := t.getFieldName(target)
	if err != nil {
		return "", err
	}

	return field + " EXISTS", nil
}

// getFieldName extracts the field name from a node.
func (t *FilterTranslator) getFieldName(node filter.Node) (string, error) {
	// A call can reach here with no target at all — `contains()` parses into a
	// CallNode whose Target is nil — and accepting a nil Node panics. That
	// makes a filter expression a client controls able to crash the process, so
	// the absent target is reported as the malformed expression it is.
	if err := filter.CheckFieldReference(node); err != nil {
		return "", err
	}

	result, err := node.Accept(visitor{t})
	if err != nil {
		return "", err
	}

	field, ok := result.(string)
	if !ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "expected field name, got %T", result)
	}

	return field, nil
}

// acceptString visits a node expected to produce a filter clause string.
// A bare identifier is treated as a boolean equality (`field = true`).
func (t *FilterTranslator) acceptString(node filter.Node) (string, error) {
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

		return field + " = true", nil
	}

	// A literal is a value, never a predicate. visitLiteral returns the value
	// itself, so a string literal comes back as a Go string — which the type
	// assertion below cannot tell apart from a rendered clause, and the
	// expression `"x OR y"` would reach Meilisearch as filter syntax rather
	// than as data.
	if lit, ok := node.(*filter.LiteralNode); ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "expected filter clause, got literal %T", lit.Value)
	}

	result, err := node.Accept(visitor{t})
	if err != nil {
		return "", err
	}

	s, ok := result.(string)
	if !ok {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "expected filter clause, got %T", result)
	}

	return s, nil
}

// formatLiteral renders a Go value as a Meilisearch filter literal.
func (t *FilterTranslator) formatLiteral(v any) (string, error) {
	switch val := v.(type) {
	case bool:
		if val {
			return "true", nil
		}

		return "false", nil
	case string:
		return t.quote(val)
	case []byte:
		return t.quote(string(val))
	case int64:
		return strconv.FormatInt(val, 10), nil
	case uint64:
		return strconv.FormatUint(val, 10), nil
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64), nil
	case time.Time:
		return epochSeconds(val), nil
	default:
		return "", coreerrs.Wrapf(filter.ErrUnsupportedType, "%T", v)
	}
}

// epochSeconds renders ts as Unix epoch seconds with the nanoseconds as an exact decimal fraction without trailing zeros. Meilisearch
// parses a filter number into the same f64 it stores a numeric attribute as, so `created < 1672531199.5` keeps the boundary that
// truncating to whole seconds would move: a record stored at 1672531199 still matches.
func epochSeconds(ts time.Time) string {
	const nanosPerSecond = int64(time.Second)

	sec, nsec := ts.Unix(), int64(ts.Nanosecond())
	if nsec == 0 {
		return strconv.FormatInt(sec, 10)
	}

	sign := ""
	if sec < 0 {
		// -1.75 is Unix() -2 with 250ms: borrow one second so the fraction reads as the magnitude.
		sign, sec, nsec = "-", -(sec + 1), nanosPerSecond-nsec
	}

	frac := strings.TrimRight(fmt.Sprintf("%09d", nsec), "0")

	return sign + strconv.FormatInt(sec, 10) + "." + frac
}

// isSizeCall reports whether n is a `size(x)` / `x.size()` call.
func isSizeCall(n filter.Node) bool {
	call, ok := n.(*filter.CallNode)
	return ok && call.Op == filter.OpSize
}

// quote renders s as a double-quoted filter string for the configured server: [quoteString] from Meilisearch 1.4, which
// unescapes `\\` in a filter string, and [quoteLegacyString] before.
func (t *FilterTranslator) quote(s string) (string, error) {
	if t.features.backslashEscape {
		return quoteString(s), nil
	}

	return quoteLegacyString(s)
}

// quoteString wraps a string in double quotes, escaping `\` and `"`. Meilisearch 1.4 and later read the result back as s: the
// filter parser first replaces `\"` with `"`, then unescapes `\\` to `\`, and a `\` always consumes the next character while
// it looks for the closing quote.
func quoteString(s string) string {
	const surroundingQuotes = 2 // opening + closing `"`

	var b strings.Builder
	b.Grow(len(s) + surroundingQuotes)
	b.WriteByte('"')

	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		default:
			b.WriteRune(r)
		}
	}

	b.WriteByte('"')

	return b.String()
}

// quoteLegacyString wraps a string in double quotes for Meilisearch 1.0 to 1.3, whose filter parser unescapes only `\"`: a
// backslash is written as it is, since `\\` would be read as two, and `"` as `\"`. A `\` still consumes the next character
// while the parser looks for the closing quote, so a run of an odd number of backslashes before a quote or at the end of s has
// no spelling there and fails with [filter.ErrUnsupportedOperation].
func quoteLegacyString(s string) (string, error) {
	const surroundingQuotes = 2 // opening + closing `"`

	var b strings.Builder
	b.Grow(len(s) + surroundingQuotes)
	b.WriteByte('"')

	run := 0 // backslashes just written

	for _, r := range s {
		switch r {
		case '\\':
			run++

			b.WriteByte('\\')

			continue
		case '"':
			if run%2 == 1 {
				return "", errLegacyBackslash()
			}

			b.WriteString(`\"`)
		default:
			b.WriteRune(r)
		}

		run = 0
	}

	if run%2 == 1 {
		return "", errLegacyBackslash()
	}

	b.WriteByte('"')

	return b.String(), nil
}

// errLegacyBackslash reports a string Meilisearch before 1.4 cannot express.
func errLegacyBackslash() error {
	return coreerrs.Wrap(filter.ErrUnsupportedOperation,
		"a string with an odd run of backslashes before a quote or at its end needs Meilisearch 1.4 or later")
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

// encode applies the configured literal encoder to v.
func (t *FilterTranslator) encode(v any) (any, error) {
	if t.literalEncoder == nil {
		return v, nil
	}

	return t.literalEncoder(v)
}
