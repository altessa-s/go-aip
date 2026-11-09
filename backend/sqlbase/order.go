// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"errors"
	"strings"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/orderby"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// ColumnResolver resolves a trusted storage name to SQL for [TranslateOrder] and [TranslateProjection]; an
// [OrderDialect] and a [ProjectionDialect] are resolvers. Implementations must quote identifiers or return
// application-owned expressions, never raw request text. Implementations shared between translators must be safe
// for concurrent use.
type ColumnResolver interface {
	Table(name string) (string, error)
}

// OrderTranslator converts an [orderby.Spec] into the ORDER BY body of one SQL
// [OrderDialect]. It is safe for concurrent use after construction.
type OrderTranslator struct {
	config    *orderby.TranslatorContext
	telemetry *orderby.Telemetry
	dialect   OrderDialect
}

// NewOrderTranslator creates a sort translator for dialect bound to policy, the client-facing [orderby.Policy] shared by every backend
// of the resource; [WithOrderOptions] binds it to the storage names of this backend. Returns [orderby.ErrPolicyRequired] for a nil
// policy and [orderby.ErrInvalidFieldPath] for the zero Dialect.
func NewOrderTranslator(policy *orderby.Policy, dialect OrderDialect, opts ...OrderOption) (*OrderTranslator, error) {
	if dialect.Identifiers.Quote == 0 {
		return nil, coreerrs.Wrap(orderby.ErrInvalidFieldPath, "zero sqlbase.OrderDialect")
	}

	o := newOrderOptions(opts...)

	ctx, err := orderby.NewTranslatorContext(policy, o.orderOptions...)
	if err != nil {
		return nil, err
	}

	name := dialect.Name
	if name == "" {
		name = unnamedDialect
	}

	return &OrderTranslator{config: ctx, dialect: dialect, telemetry: orderby.NewTelemetry(o.collector, name)}, nil
}

// Translate converts an [orderby.Spec] into the body of an ORDER BY clause,
// for example `"created_at" DESC, "slug" ASC` on PostgreSQL. An empty Spec
// translates to "".
func (t *OrderTranslator) Translate(ob orderby.Spec) (string, error) {
	done := t.telemetry.Start()
	defer done()

	return TranslateOrder(t.config, t.dialect, ob)
}

// keySizeHint is the expected rendered size of one key: a quoted column of a
// dozen characters, a direction and a separator.
const keySizeHint = 24

// TranslateOrder renders ob as the body of an ORDER BY clause — `col DIR, …`,
// without the keywords — through style, which is an [OrderDialect] or an
// application resolver. Each key must be allowed by bound, and under a
// [QualifiedNamesDialect] a dotted key must be declared
// ([orderby.TranslatorContext.DeclaresField]) or it fails with
// [orderby.ErrFieldNotAllowed]; style resolves its
// mapped name, and a key with null placement or collation is rendered by
// style's [KeyRenderer], or rejected with [orderby.ErrUnsupportedOrdering]
// when style has none. An empty spec renders "".
func TranslateOrder(bound *orderby.TranslatorContext, style ColumnResolver, ob orderby.Spec) (string, error) {
	if bound == nil || nilcheck.IsNil(style) {
		return "", orderby.ErrInvalidFieldPath
	}

	resolved, err := bound.Resolve(ob)
	if err != nil {
		return "", err
	}

	for _, k := range ob.Keys {
		if err := checkDeclared(style, k.Name, bound.DeclaresField); err != nil {
			return "", coreerrs.Wrapf(orderby.ErrFieldNotAllowed, "%s: %w", k.Name, err)
		}
	}

	ob = resolved
	if ob.IsEmpty() {
		return "", nil
	}

	var b strings.Builder
	b.Grow(len(ob.Keys) * keySizeHint)

	for i, k := range ob.Keys {
		col, err := style.Table(k.Name)
		if err != nil {
			// The column position cannot be bound as a parameter, so a
			// mapping to anything but an identifier — an expression, a JSON
			// path, a quote — is rejected rather than spliced in.
			return "", coreerrs.Wrapf(orderby.ErrInvalidFieldPath, "%s is not a SQL column name: %w", k.Name, err)
		}

		if k.Nulls != orderby.NullsDefault || k.Collation != "" {
			renderer, ok := style.(KeyRenderer)
			if !ok {
				return "", orderby.ErrUnsupportedOrdering
			}

			col, err = renderer.RenderKey(col, k)
			if err != nil {
				return "", err
			}

			if i > 0 {
				b.WriteString(", ")
			}

			b.WriteString(col)

			continue
		}

		if i > 0 {
			b.WriteString(", ")
		}

		b.WriteString(col)

		if k.IsDescending() {
			b.WriteString(" DESC")
		} else {
			b.WriteString(" ASC")
		}
	}

	return b.String(), nil
}

// errUndeclaredQualifier explains why a dotted name the policy does not declare is refused by a [QualifiedNamesDialect].
var errUndeclaredQualifier = errors.New("a dotted field names a table qualifier and must be declared (WithAllowedFields or WithFieldMapping)")

// checkDeclared refuses, with errUndeclaredQualifier, a dotted client name the policy does not declare when style reads a dot
// as a table qualifier ([QualifiedNamesDialect]): a name the client composed under a wildcard allow-list or a prefix mapping
// could otherwise reach a column of another joined table. Application resolvers do not qualify.
func checkDeclared(style ColumnResolver, name string, declares func(string) bool) error {
	q, ok := style.(QualifiedNamesDialect)
	if !ok || !q.QualifiesNames() || !strings.Contains(name, ".") || declares(name) {
		return nil
	}

	return errUndeclaredQualifier
}

// KeyRenderer optionally renders a complete key, including direction, null order
// and collation. The column has already been resolved; collation is untrusted.
type KeyRenderer interface {
	RenderKey(column string, key orderby.Key) (string, error)
}
