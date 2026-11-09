// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-atlas-core/encoding/keys"

	"github.com/altessa-s/go-aip/apierror"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/page"
	"github.com/altessa-s/go-aip/pagination/pagetoken"

	corecontext "github.com/altessa-s/go-atlas-core/context"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// listPlan is the order-dependent part of a list query: the physical cursor keys, ending with the identity, and the
// static SQL around the WHERE clause.
type listPlan struct {
	keys []ListOrderKey
	// selectSQL is "SELECT <identity>, <sort keys>, <projection> FROM <table> AS <alias> WHERE ".
	selectSQL string
	// orderSQL is " ORDER BY <keys>".
	orderSQL string
}

// compilePlans builds the plans of the declared orders. A declared order that fails validation, which construction
// already rejects, is left to the per-request path so that it fails with the request error.
func (l *ListEngine[T]) compilePlans() {
	orders := append(l.schema.Orders(), l.schema.Default())
	l.plans = make(map[string]*listPlan, len(orders))

	for _, o := range orders {
		if p, err := l.newPlan(o); err == nil {
			l.plans[o.String()] = p
		}
	}
}

// plan returns the precompiled plan of a declared order, or builds the plan of an unrestricted order.
func (l *ListEngine[T]) plan(order listing.Order) (*listPlan, error) {
	if p, ok := l.plans[order.String()]; ok {
		return p, nil
	}

	return l.newPlan(order)
}

func (l *ListEngine[T]) newPlan(order listing.Order) (*listPlan, error) {
	keysOrder, err := l.validateOrder(order)
	if err != nil {
		return nil, err
	}

	return l.buildSelect(keysOrder), nil
}

// validateOrder runs before filter parsing and translation. It returns the physical cursor keys of order.
func (l *ListEngine[T]) validateOrder(order listing.Order) ([]ListOrderKey, error) {
	for _, key := range order.Keys() {
		if key.Nulls != 0 || key.Collation != "" {
			return nil, l.schema.FormatError(listing.FieldOrderBy, "null placement and collation are not supported", listing.ErrInvalidSchema)
		}
	}

	keysOrder := l.orderKeys(order)
	if len(keysOrder) == 0 {
		return nil, l.schema.FormatError(listing.FieldOrderBy, "id may only appear as the last sort key", listing.ErrInvalidSchema)
	}

	seenColumns := map[string]bool{}

	for _, key := range l.opts.scopeKeys {
		seenColumns[l.column(key)] = true
	}

	for _, key := range keysOrder {
		if seenColumns[key.Column] {
			return nil, l.schema.FormatError(listing.FieldOrderBy, "duplicate or scope sort column", listing.ErrInvalidSchema)
		}

		seenColumns[key.Column] = true
	}

	return keysOrder, nil
}

// buildSelect renders the SELECT list, FROM and ORDER BY text of keysOrder. The projection columns were validated at
// construction.
func (l *ListEngine[T]) buildSelect(keysOrder []ListOrderKey) *listPlan {
	alias := l.quote(l.opts.tableAlias)

	var sel strings.Builder

	sel.WriteString("SELECT ")
	sel.WriteString(l.opts.dialect.IdentityExpression(l.quote(l.opts.idColumn), l.opts.kindResolver(l.opts.idKind)))

	for _, k := range keysOrder[:len(keysOrder)-1] {
		sel.WriteString(", ")
		sel.WriteString(l.sortKeySelect(k))
	}

	for _, c := range l.columns {
		sel.WriteString(", ")
		sel.WriteString(l.quote(c))
	}

	sel.WriteString(" FROM ")
	sel.WriteString(l.quotedTable)
	sel.WriteString(" AS ")
	sel.WriteString(alias)
	sel.WriteString(" WHERE ")

	var ord strings.Builder

	ord.WriteString(" ORDER BY ")

	for i, k := range keysOrder {
		if i > 0 {
			ord.WriteString(", ")
		}

		ord.WriteString(alias)
		ord.WriteByte('.')
		ord.WriteString(l.quote(k.Column))

		if k.Desc {
			ord.WriteString(" DESC")
		} else {
			ord.WriteString(" ASC")
		}
	}

	return &listPlan{keys: keysOrder, selectSQL: sel.String(), orderSQL: ord.String()}
}

// List returns a page restricted to scope and the request filter. The page
// token binds the subject, parent, canonical filter, order and canonical scope.
func (l *ListEngine[T]) List(ctx context.Context, subject string, req listing.Request, scope Scope) (page.Page[T], error) {
	var plan *listPlan

	q, err := l.schema.CompileWithOrderValidator(ctx, req, func(o listing.Order) error {
		var perr error

		plan, perr = l.plan(o)

		return perr
	})
	if err != nil {
		return page.Page[T]{}, err
	}

	pins, scopeKey, err := l.canonicalScope(scope, req.Parent)
	if err != nil {
		return page.Page[T]{}, err
	}

	filterSQL, args := "", []any{}
	if q.Filter != nil {
		filterSQL, args, err = l.translator.Translate(q.Filter)
		if err != nil {
			return page.Page[T]{}, l.schema.TranslationError(err)
		}
	}

	// The bindings need the canonical filter: they are computed only to resolve or issue a token.
	var (
		binds pagetoken.Bindings
		pos   *pagetoken.Position
	)

	if req.PageToken != "" {
		if binds, err = l.bindings(subject, req.Parent, scopeKey, q); err != nil {
			return page.Page[T]{}, err
		}

		resolved, first, e := l.tokens.Resolve(req.PageToken, binds)
		if e != nil {
			return page.Page[T]{}, l.schema.FormatError(listing.FieldPageToken, pagetoken.Classify(e).String(), e)
		}

		if !first {
			pos = &resolved
		}
	}

	where, args, err := l.buildWhere(filterSQL, args, pins, plan.keys, pos)
	if err != nil {
		return page.Page[T]{}, err
	}

	query := l.opts.dialect.LimitSelect(plan.selectSQL+where+plan.orderSQL, q.Size+1)

	qctx, cancel := corecontext.WithMaxTimeout(ctx, l.opts.maxTime)
	defer cancel()

	rows, err := l.q.QueryContext(qctx, query, args...)
	if err != nil {
		return page.Page[T]{}, l.mapError(ctx, qctx, err, q.Filter != nil)
	}
	defer func() { _ = rows.Close() }()

	items, last, hasMore, err := l.scanPage(rows, plan.keys, q.Size)
	if err != nil {
		// The query context ending closes the rows under the scan: that is a timeout or a cancellation, not a stored row, and the
		// scan error stays hidden whatever it is.
		if qctx.Err() != nil {
			return page.Page[T]{}, l.mapEndedError(ctx, qctx, err, q.Filter != nil)
		}

		// A destination's Scan may return any error, a token or deadline sentinel included: it is still a stored row that does
		// not read, which an API error mapper classifies as such and not by that cause.
		return page.Page[T]{}, apierror.Classify(ErrCorruptRecord, err)
	}

	if err = rows.Err(); err != nil {
		return page.Page[T]{}, l.mapError(ctx, qctx, err, q.Filter != nil)
	}

	out := page.Page[T]{Items: items}
	// A list without a codec serves a single page: it issues no token.
	if hasMore && l.codec != nil {
		if out.NextPageToken, err = l.nextToken(last, binds, req.PageToken != "", subject, req.Parent, scopeKey, q); err != nil {
			// A signer or position codec failing once the query context ended reports that end alone, as a scan does: its error,
			// a field or API error included, stays hidden and selects no status.
			if qctx.Err() != nil {
				return page.Page[T]{}, l.mapEndedError(ctx, qctx, err, q.Filter != nil)
			}

			return page.Page[T]{}, err
		}
	}

	return out, nil
}

// bindings returns the token bindings of a request.
func (l *ListEngine[T]) bindings(subject, parent, scopeKey string, q listing.Query) (pagetoken.Bindings, error) {
	canonicalFilter, err := filter.Canonical(q.Filter)
	if err != nil {
		return pagetoken.Bindings{}, l.schema.TranslationError(err)
	}

	return pagetoken.NewBindings(keys.Compose(subject, parent), q.Order.String(), canonicalFilter, scopeKey, l.schema.ContractID(),
		l.opts.positionCodec.Name(), l.opts.timestampPrecision.String(), l.opts.idColumn), nil
}

// nextToken issues the token continuing after last, reusing the bindings computed to resolve the request token.
func (l *ListEngine[T]) nextToken(last pagetoken.Position, binds pagetoken.Bindings, bound bool, subject, parent, scopeKey string,
	q listing.Query,
) (string, error) {
	if !bound {
		var err error
		if binds, err = l.bindings(subject, parent, scopeKey, q); err != nil {
			return "", err
		}
	}

	return l.tokens.Issue(last, binds)
}

// buildWhere completes the WHERE clause: the translated filter, the scope pins and, when resuming after pos, the
// cursor predicate. Each part is parenthesized; an empty clause is TRUE.
func (l *ListEngine[T]) buildWhere(filterSQL string, args []any, pins []pin, keysOrder []ListOrderKey,
	pos *pagetoken.Position,
) (string, []any, error) {
	var b strings.Builder

	clause := func(s string) {
		if b.Len() > 0 {
			b.WriteString(" AND ")
		}

		b.WriteByte('(')
		b.WriteString(s)
		b.WriteByte(')')
	}

	if filterSQL != "" {
		clause(filterSQL)
	}

	for _, p := range pins {
		clause(l.opts.dialect.Bind(l.quote(p.key)+" = ?", len(args)+1))
		args = append(args, p.value)
	}

	if pos != nil {
		pred, vals, err := l.opts.dialect.CursorPredicate(keysOrder, *pos, l.opts.tableAlias, l.opts.idType, l.opts.decimalFormat)
		if err != nil {
			return "", nil, l.schema.FormatError(listing.FieldPageToken, pagetoken.Classify(err).String(), err)
		}

		clause(l.opts.dialect.Bind(pred, len(args)+1))
		args = append(args, vals...)
	}

	if b.Len() == 0 {
		return "TRUE", args, nil
	}

	return b.String(), args, nil
}

// scanPage scans the rows of a page of size items plus one look-ahead row. It returns at most size items, whether
// more rows exist and, when the engine issues tokens, the position of the item at index size-1. The scan destinations
// of the cursor keys are shared by all rows; every other row only passes the cheap boundary checks of checkRow.
func (l *ListEngine[T]) scanPage(rows *sql.Rows, keysOrder []ListOrderKey, size int) ([]T, pagetoken.Position, bool, error) {
	n := len(keysOrder)

	sinks := make([]any, n, n+len(l.columns))
	sinks[0] = sortDestination(keysOrder[n-1].StorageKind)

	for i, k := range keysOrder[:n-1] {
		sinks[i+1] = sortDestination(k.StorageKind)
	}

	items := make([]T, 0, size+1)

	var last pagetoken.Position

	for rows.Next() {
		var zero T

		items = append(items, zero)
		sinks = append(sinks[:n], l.dest(&items[len(items)-1])...)

		if err := rows.Scan(sinks...); err != nil {
			return nil, pagetoken.Position{}, false, coreerrs.Wrap(err, "scan listing row")
		}

		var err error
		if len(items) == size && l.codec != nil {
			last, err = l.position(sinks[:n], keysOrder)
		} else {
			err = l.checkRow(sinks[:n], keysOrder)
		}

		if err != nil {
			return nil, pagetoken.Position{}, false, err
		}
	}

	hasMore := len(items) > size
	if hasMore {
		items = items[:size]
	}

	return items, last, hasMore, nil
}

// position builds the cursor position of a scanned row. dests holds the identity destination followed by those of
// the non-identity keys of keysOrder.
func (l *ListEngine[T]) position(dests []any, keysOrder []ListOrderKey) (pagetoken.Position, error) {
	identity, err := l.scannedSortValue(dests, keysOrder, 0)
	if err != nil {
		return pagetoken.Position{}, err
	}

	p := pagetoken.Position{ID: identity}
	if n := len(dests) - 1; n > 0 {
		p.Keys = make([]pagetoken.SortValue, 0, n)
	}

	for i := 1; i < len(dests); i++ {
		sv, err := l.scannedSortValue(dests, keysOrder, i)
		if err != nil {
			return pagetoken.Position{}, err
		}

		p.Keys = append(p.Keys, sv)
	}

	return p, nil
}

// checkRow applies the boundary checks of a row whose position is not built: a timestamp must be aligned to the
// storage precision, and a value of a custom kind must normalize.
func (l *ListEngine[T]) checkRow(dests []any, keysOrder []ListOrderKey) error {
	for i, dest := range dests {
		switch d := dest.(type) {
		case *time.Time:
			if err := l.alignedTimestamp(d.UTC()); err != nil {
				if i == 0 {
					return err
				}

				return coreerrs.Wrap(err, "decode sort key")
			}
		case *any:
			if _, err := l.scannedSortValue(dests, keysOrder, i); err != nil {
				return err
			}
		}
	}

	return nil
}

// scannedSortValue returns the sort value of dests[i]: the identity for i == 0, else the key keysOrder[i-1].
func (l *ListEngine[T]) scannedSortValue(dests []any, keysOrder []ListOrderKey, i int) (pagetoken.SortValue, error) {
	if i == 0 {
		id, err := scannedIdentity(dests[0], keysOrder[len(keysOrder)-1].StorageKind)
		if err != nil {
			return pagetoken.SortValue{}, err
		}

		return l.sortValue(id)
	}

	v := deref(dests[i])
	if keysOrder[i-1].StorageKind == Numeric {
		text, _ := v.(string) // a Numeric sort key is scanned as its decimal text

		d, err := decimal.NewFromString(text)
		if err != nil {
			return pagetoken.SortValue{}, coreerrs.Wrap(err, "decode decimal sort key")
		}

		v = d
	}

	sv, err := l.sortValue(v)
	if err != nil {
		return pagetoken.SortValue{}, coreerrs.Wrap(err, "decode sort key")
	}

	return sv, nil
}

// scannedIdentity returns the identity held by dest: bytes as a string and a Numeric identity as a decimal.
func scannedIdentity(dest any, kind Kind) (any, error) {
	id := deref(dest)
	if b, ok := id.([]byte); ok {
		id = string(b)
	}

	if kind != Numeric {
		return id, nil
	}

	text, ok := id.(string)
	if !ok {
		text = fmt.Sprint(id)
	}

	return decimal.NewFromString(text)
}
