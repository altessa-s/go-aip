// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory

import (
	"context"
	"fmt"
	"math"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/evaluation"
	"github.com/altessa-s/go-aip/pagination/page"
	"github.com/altessa-s/go-aip/pagination/pagetoken"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// ListEngine serves a list from a slice held in memory (a small catalog or a
// code-defined collection) with the semantics of every listing engine: the
// same request validation, the same orders with the id tie-breaker, and
// keyset page tokens (pagetoken.Typed over a pagetoken.Position)
// carrying the last item's sort values and id. It is safe for concurrent
// use.
//
// Every page scans and filters the whole slice it is given (O(n log k) per
// request for k the page size): ListEngine is for small collections — catalogs, code-defined lists,
// in-memory test stores — never for a collection that grows with the data.
//
// Tokens are bound to the subject, the canonical order, the canonical filter
// (filter.Canonical, so spelling does not matter) and the parent, and are
// verified before any item is read. They are
// not interchangeable with the tokens of another engine; a store uses one
// engine.
type ListEngine[T any] struct {
	opts    *listOptions
	schema  *listing.Schema
	codec   pagetoken.Signer
	id      func(T) any
	fields  func(T) map[string]any
	profile evaluator.Profile
	reader  *evaluation.Reader
}

// NewListEngine returns the in-memory list of T under schema. The codec comes
// before the schema, as in the SQL list engines. id returns an item's
// unique scalar id (the tie-breaker); fields
// returns the item's filterable values keyed by public field name, as
// stored: enum fields hold their stored value, timestamps a time.Time,
// arrays a slice, and a dotted name a nested map. Values are read through
// the memory evaluator; [WithListValueEncoding] can adapt driver-specific stored
// representations. An absent or null optional scalar reads as its kind's
// zero value, and absent, null or empty arrays read as empty. codec signs the page tokens;
// nil serves a single page and rejects a page token. A nil schema, id or
// fields fails with [listing.ErrInvalidSchema]. Requests are bounded by
// [DefaultMaxWork] unless [WithListMaxWork] sets another limit; a profile
// replaced with [WithListProfile] must implement [RequestProfile] unless
// the limit is lifted explicitly with WithListMaxWork(0).
func NewListEngine[T, ID any](
	codec pagetoken.Signer, schema *listing.Schema, id func(T) ID, fields func(T) map[string]any, opts ...ListEngineOption,
) (*ListEngine[T], error) {
	switch {
	case schema == nil:
		return nil, coreerrs.Wrap(listing.ErrInvalidSchema, "in-memory list without a schema")
	case id == nil || fields == nil:
		return nil, coreerrs.Wrap(listing.ErrInvalidSchema, "in-memory list without its id or fields function")
	}

	o := newListOptions(opts...)
	if o.tiebreakField != "" {
		f, ok := schema.Field(o.tiebreakField)
		if !ok || f.Array || f.Optional || f.Presence || f.Kind == filter.FieldKindBytes {
			return nil, coreerrs.Wrap(listing.ErrInvalidSchema, "tie-breaker must be a declared required sortable scalar")
		}
	}

	for _, order := range append(schema.Orders(), schema.Default()) {
		if err := o.checkOrder(order); err != nil {
			return nil, err
		}
	}

	if o.timestampPrecision <= 0 {
		return nil, coreerrs.Wrap(listing.ErrInvalidSchema, "nonpositive timestamp precision")
	}

	if o.maxWork < 0 || o.maxScanned < 0 || o.maxMaterialized < 0 {
		return nil, coreerrs.Wrap(listing.ErrInvalidSchema, "negative work limit")
	}

	reader := evaluation.NewReader(o.valueEncoding, o.maxDataBudget)

	profile, err := evaluation.New(schema, reader)
	if err != nil {
		return nil, err
	}

	selected := o.profile
	if nilcheck.IsNil(selected) {
		selected = profile
	}

	if o.maxWork > 0 {
		if _, ok := selected.(RequestProfile); !ok {
			return nil, coreerrs.Wrap(listing.ErrInvalidSchema, "bounded evaluation requires a RequestProfile")
		}
	}

	_, err = evaluator.New(schema.FilterPolicy(), evaluator.WithProfile(selected), evaluator.WithMaxDataBudget(o.maxDataBudget))
	if err != nil {
		return nil, err
	}

	return &ListEngine[T]{opts: o, schema: schema, codec: codec, id: func(it T) any { return id(it) }, fields: fields, profile: selected, reader: reader}, nil
}

// MustNewListEngine is [NewListEngine] for package-level declarations with
// static configuration (var widgets = memory.MustNewListEngine(nil,
// widgetSchema, widgetID, widgetFields)): it returns the engine
// NewListEngine builds and panics on an invalid configuration. The panic
// value is an error prefixed with "memory.MustNewListEngine: " that wraps
// the NewListEngine error, so errors.Is on the recovered value matches whatever NewListEngine
// returned ([listing.ErrInvalidSchema] for an invalid schema). Use NewListEngine where the
// configuration comes from input rather than code.
func MustNewListEngine[T, ID any](
	codec pagetoken.Signer, schema *listing.Schema, id func(T) ID, fields func(T) map[string]any, opts ...ListEngineOption,
) *ListEngine[T] {
	m, err := NewListEngine(codec, schema, id, fields, opts...)
	if err != nil {
		panic(fmt.Errorf("memory.MustNewListEngine: %w", err))
	}

	return m
}

// Validate checks req without listing: the page size, the parent, the order
// and the filter, with the errors of [ListEngine.List]. A caller that has to load
// the items first validates the request before paying for the load. The
// page token is checked by [ListEngine.List].
func (m *ListEngine[T]) Validate(ctx context.Context, req listing.Request) error {
	_, err := m.schema.CompileWithOrderValidator(ctx, req, m.checkOrder)
	return err
}

// List returns one page of the items matching req, in the requested order.
// items is the whole collection already restricted to the parent (when the
// list has one); it is not modified. subject names the list (the gRPC full
// method). Every invalid request field is an InvalidArgument
// core/errors.FieldError ([listing.Schema.Compile], pageToken); an item
// without a scalar value of its kind's class in a sort field is an internal
// error whose message carries no stored id or value.
//
// Matching follows the proto3 listing contract: absent or null optional
// scalars read as zero, arrays match existentially, inequality complements
// equality, and a null presence field has the default evaluator's null
// semantics, as in the SQL engines.
//
// The scan drops the matches at or before the page token's position and keeps
// the page's rows, plus one to detect the next page, in a bounded heap: a page
// costs O(n log k) comparisons for k the page size and retains O(k) rows.
func (m *ListEngine[T]) List(ctx context.Context, subject string, req listing.Request, items []T) (page.Page[T], error) {
	q, err := m.schema.CompileWithOrderValidator(ctx, req, m.checkOrder)
	if err != nil {
		return page.Page[T]{}, err
	}

	// The page token is verified against every binding before any item is scanned, so a forged or foreign token costs no
	// scan or sort.
	canonicalFilter, err := filter.Canonical(q.Filter)
	if err != nil {
		return page.Page[T]{}, m.schema.TranslationError(err)
	}

	b := pagetoken.NewBindings(subject, q.Order.String(), canonicalFilter, req.Parent, m.schema.ContractID(), m.opts.positionCodec.Name(),
		m.opts.timestampPrecision.String(), m.opts.tiebreakField)
	tokens := pagetoken.NewTyped[pagetoken.Position](m.codec, pagetoken.WithPayloadCodec(m.opts.positionCodec))

	after, first, err := tokens.Resolve(req.PageToken, b)
	if err != nil {
		return page.Page[T]{}, m.pageError(subject, err)
	}

	work := workBudget{ctx: ctx, max: m.opts.maxWork}

	profile := m.profile
	if bounded, ok := profile.(RequestProfile); ok {
		profile = bounded.BindBudget(&work)
		if nilcheck.IsNil(profile) {
			return page.Page[T]{}, coreerrs.Wrap(listing.ErrInvalidSchema, "RequestProfile returned nil")
		}
	}

	eval, err := evaluator.New(m.schema.FilterPolicy(), evaluator.WithProfile(profile),
		evaluator.WithMaxDataBudget(m.opts.maxDataBudget))
	if err != nil {
		return page.Page[T]{}, err
	}

	prepared, err := eval.Prepare(q.Filter)
	if err != nil {
		// The built-in profile charges folded needles during preparation. A custom profile owns its preparation errors.
		if work.err != nil && nilcheck.IsNil(m.opts.profile) {
			return page.Page[T]{}, work.err
		}

		return page.Page[T]{}, err
	}

	if err = work.take(); err != nil {
		return page.Page[T]{}, err
	}

	if m.opts.maxScanned > 0 && len(items) > m.opts.maxScanned {
		return page.Page[T]{}, ErrWorkBudget
	}

	size := max(q.Size, 1)

	scan := m.newScan(q.Order, m.reader.BindBudget(&work), &work)
	if q.Filter != nil {
		scan.filter = prepared
	}

	top := topRows[T]{compare: comparePositions(q.Order, m.opts.positionCodec, m.opts), work: &work, limit: size + 1,
		maxMaterialized: m.opts.maxMaterialized}

	for _, it := range items {
		if err = scan.offer(it, &top, after, first); err != nil {
			return page.Page[T]{}, err
		}
	}

	rows, err := top.sorted()
	if err != nil {
		return page.Page[T]{}, err
	}

	end := min(size, len(rows))

	out := page.Page[T]{Items: make([]T, end)}
	for i, r := range rows[:end] {
		out.Items[i] = r.item
	}

	if end < len(rows) {
		if out.NextPageToken, err = tokens.Issue(rows[end-1].pos, b); err != nil {
			return page.Page[T]{}, m.pageError(subject, err)
		}
	}

	return out, nil
}

// pageError reports a page token failure as an InvalidArgument pageToken field error and any other paging failure as internal.
func (m *ListEngine[T]) pageError(subject string, err error) error {
	if failure := pagetoken.Classify(err); failure != pagetoken.FailureNone {
		return m.schema.FormatError(pagetoken.TokenField, failure.String(), err)
	}

	return coreerrs.Wrapf(err, "memory: page %s", subject)
}

// scanKey is a sort key of a request with its declared field, resolved once per request.
type scanKey struct {
	field    string
	kind     filter.FieldKind
	expected pagetoken.SortClass
}

// scan computes the page positions of a request's items into one reused position.
type scan[T any] struct {
	m      *ListEngine[T]
	reader evaluation.FieldReader
	work   *workBudget
	// filter is the prepared request filter; nil matches every item.
	filter *evaluator.Prepared
	keys   []scanKey
	// idKind is the kind of the tie-breaker field, when one replaces the id callback.
	idKind filter.FieldKind
	// pos is the position of the last item, valid until the next call; a retained row copies it.
	pos pagetoken.Position
}

// newScan resolves the sort keys of o and their fields once for the request.
func (m *ListEngine[T]) newScan(o listing.Order, reader evaluation.FieldReader, work *workBudget) *scan[T] {
	keys := m.opts.sortKeys(o)

	s := &scan[T]{m: m, reader: reader, work: work, keys: make([]scanKey, len(keys)), pos: pagetoken.Position{Keys: make([]pagetoken.SortValue, len(keys))}}
	for i, k := range keys {
		f, _ := m.schema.FieldInfo(k.Field)
		s.keys[i] = scanKey{field: k.Field, kind: f.Kind, expected: memorySortClass(f.Kind)}
	}

	if m.opts.tiebreakField != "" {
		f, _ := m.schema.FieldInfo(m.opts.tiebreakField)
		s.idKind = f.Kind
	}

	return s
}

// offer scans one item: it charges the scan, filters the item, computes its position and offers a match after the page token's
// position to top.
func (s *scan[T]) offer(it T, top *topRows[T], after pagetoken.Position, first bool) error {
	if err := s.work.take(); err != nil {
		return err
	}

	values := s.m.fields(it)
	if s.filter != nil {
		ok, err := s.filter.Evaluate(values)

		if s.work.err != nil {
			return s.work.err
		}

		if err != nil {
			// The id of a stored item stays out of the message: the error can reach the caller.
			return coreerrs.Wrap(err, "memory: filter an item")
		}

		if !ok {
			return nil
		}
	}

	err := s.position(it, values)

	if s.work.err != nil {
		return s.work.err
	}

	if err != nil {
		return err
	}

	// A match at or before the token's position belongs to an earlier page.
	if !first {
		if err := s.work.take(); err != nil {
			return err
		}

		if top.compare(s.pos, after) <= 0 {
			return nil
		}
	}

	return top.offer(it, &s.pos)
}

// position computes the page position of an item into s.pos: its sort values, each of the class of its field's kind, and its id.
func (s *scan[T]) position(it T, values map[string]any) error {
	m := s.m

	var id any

	if m.opts.tiebreakField != "" {
		var err error

		id, _, err = s.reader.Lookup(values, m.opts.tiebreakField)
		if err != nil {
			return err
		}
	} else {
		id = m.id(it)
	}

	identity, err := m.opts.positionCodec.Normalize(id, m.opts.timestampPrecision)
	if err != nil {
		return err
	}

	if number, ok := id.(float64); ok && (math.IsNaN(number) || math.IsInf(number, 0)) {
		return fmt.Errorf("%w: non-finite identity", pagetoken.ErrUnsortableValue)
	}

	if !identity.Valid() {
		return fmt.Errorf("%w: null identity", pagetoken.ErrUnsortableValue)
	}

	if m.opts.tiebreakField != "" {
		expected := memorySortClass(s.idKind)
		if expected != pagetoken.SortClassNone && identity.Class() != pagetoken.SortClassNone && identity.Class() != expected {
			return fmt.Errorf("%w: identity does not sort as %s", pagetoken.ErrUnsortableValue, s.idKind)
		}
	}

	s.pos.ID = identity

	for i, k := range s.keys {
		value, err := s.value(values, k)
		s.pos.Keys[i] = value

		if err != nil {
			return coreerrs.Wrapf(err, "memory: sort field %q of an item", k.field)
		}
	}

	return nil
}

func (s *scan[T]) value(values map[string]any, k scanKey) (pagetoken.SortValue, error) {
	raw, _, err := s.reader.Lookup(values, k.field)
	if err != nil {
		return pagetoken.SortValue{}, err
	}

	if number, ok := raw.(float64); k.kind == filter.FieldKindFloat && ok && (math.IsNaN(number) || math.IsInf(number, 0)) {
		return pagetoken.SortValue{}, fmt.Errorf("%w: non-finite float", pagetoken.ErrUnsortableValue)
	}

	value, err := s.m.opts.positionCodec.Normalize(raw, s.m.opts.timestampPrecision)
	if err != nil {
		return pagetoken.SortValue{}, err
	}

	if k.expected != pagetoken.SortClassNone && value.Class() != pagetoken.SortClassNone && value.Class() != k.expected {
		return pagetoken.SortValue{}, fmt.Errorf("%w: a %s value does not sort as %s", pagetoken.ErrUnsortableValue, value.Class(), k.kind)
	}

	return value, nil
}

func memorySortClass(kind filter.FieldKind) pagetoken.SortClass {
	switch kind {
	case filter.FieldKindString:
		return pagetoken.SortClassString
	case filter.FieldKindInt, filter.FieldKindFloat, filter.FieldKindDecimal:
		return pagetoken.SortClassNumber
	case filter.FieldKindBool:
		return pagetoken.SortClassBool
	case filter.FieldKindTimestamp:
		return pagetoken.SortClassTime
	default:
		return pagetoken.SortClassNone
	}
}

// comparePositions returns the order of positions under o: key by key in
// each key's direction, then by id in the direction of the last key — the
// order every listing engine serves.
func comparePositions(o listing.Order, comparator pagetoken.ValueComparator, opts *listOptions) func(a, b pagetoken.Position) int {
	keys := opts.sortKeys(o)

	desc := make([]bool, len(keys))
	for i, k := range keys {
		desc[i] = k.Desc
	}

	allKeys := o.Keys()
	descendingID := len(allKeys) > 0 && allKeys[len(allKeys)-1].Desc

	return pagetoken.ComparePositions(desc, descendingID, comparator)
}

// RequestProfile binds a custom evaluator to the request-wide scan, read,
// predicate and sorting budget. Implementations must charge their nested work.
// BindBudget must return a request-owned profile without mutating the receiver.
type RequestProfile interface {
	BindBudget(evaluator.Budget) evaluator.Profile
}

func (o *listOptions) identityField() string {
	return o.tiebreakField
}

func (o *listOptions) checkOrder(order listing.Order) error {
	keys := order.Keys()
	for i, k := range keys {
		if k.Nulls != 0 || k.Collation != "" {
			return coreerrs.Wrap(listing.ErrInvalidSchema, "null placement and collation are not supported")
		}

		if k.Field == o.identityField() && i != len(keys)-1 {
			return coreerrs.Wrap(listing.ErrInvalidSchema, "id may only appear as the last sort key")
		}
	}

	return nil
}

func (o *listOptions) sortKeys(order listing.Order) []listing.OrderKey {
	keys := order.Keys()
	if len(keys) > 0 && keys[len(keys)-1].Field == o.identityField() {
		return keys[:len(keys)-1]
	}

	return keys
}

func (m *ListEngine[T]) checkOrder(order listing.Order) error {
	if err := m.opts.checkOrder(order); err != nil {
		return m.schema.FormatError(listing.FieldOrderBy, "unsupported order", err)
	}

	return nil
}
