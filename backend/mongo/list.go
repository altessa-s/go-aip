// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/altessa-s/go-atlas-core/encoding/keys"
	"github.com/altessa-s/go-mongox/keyset"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/page"
	"github.com/altessa-s/go-aip/pagination/pagetoken"

	corecontext "github.com/altessa-s/go-atlas-core/context"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// DefaultTiebreakPath is the unique BSON identity appended to a sort when WithListTiebreakPath is not supplied.
const DefaultTiebreakPath = "_id"

// IDIndex is the name of MongoDB's built-in _id index, which backs an unscoped id-only order; [ListEngine.Indexes] never returns
// it.
const IDIndex = "_id_"

// MaxListPageSize is the largest page a list engine reads: a schema whose maximum page size exceeds it is refused.
const MaxListPageSize = keyset.MaxPageSize

// ListEngine serves a list from a MongoDB collection with keyset pages read by a plain find, decoding documents into D. It is
// safe for concurrent use; build one per collection and share it.
type ListEngine[D any] struct {
	coll       *mongo.Collection
	codec      pagetoken.Signer
	schema     *listing.Schema
	opts       *listOptions
	translator *FilterTranslator
	orders     map[string]listOrder // canonical orderBy -> sort and index
	indexes    []indexSpec
	// presence and arrays are the schema's Presence and array fields, by name, for [presenceForm]; presence is empty when the
	// schema declares none.
	presence, arrays map[string]bool
}

// caseInsensitiveContainsSchema is a listing schema that can select fields for case-insensitive contains. The engine asks through
// this interface, not through the schema type, because a release of this module also builds against a root module released before
// the option existed; such a schema selects no field, so there is nothing to refuse.
type caseInsensitiveContainsSchema interface {
	CaseInsensitiveContainsFields() []string
}

// NewListEngine returns the list engine of coll under schema. codec signs the page tokens; nil serves a single page and rejects
// a page token.
//
// coll may be nil for an engine that only plans: [ListEngine.Sort], [ListEngine.Index] and [ListEngine.Indexes] read the
// compiled orders and never touch the collection, so a store can resolve its sorts and index models without a connection.
// [ListEngine.List] and [ListEngine.EnsureIndexes] need the collection.
//
// It fails with [listing.ErrInvalidSchema] for a nil schema, a schema without declared sort patterns or whose maximum page size
// exceeds [MaxListPageSize], a field path or sort path that is not a BSON path or names an undeclared field, a sort path or
// scope key on the id, a scope key that is not a BSON path, an order that sorts on the id anywhere but last, twice on one path
// or on a scope key, and an index name that is not keyed by a declared pattern, names a pattern twice, is reused, is the
// built-in [IDIndex] of another order or renames it.
func NewListEngine[D any](coll *mongo.Collection, codec pagetoken.Signer, schema *listing.Schema, opts ...ListEngineOption) (*ListEngine[D], error) {
	if schema == nil {
		return nil, invalid("MongoDB list without a schema")
	}

	if folding, ok := any(schema).(caseInsensitiveContainsSchema); ok && len(folding.CaseInsensitiveContainsFields()) != 0 {
		return nil, invalid("MongoDB does not support case-insensitive contains fields")
	}

	if schema.UnrestrictedOrder() || len(schema.Orders()) == 0 {
		return nil, invalid("MongoDB requires declared nonempty sort patterns")
	}

	l := &ListEngine[D]{coll: coll, codec: codec, schema: schema, opts: newListOptions(opts...), orders: map[string]listOrder{}}
	if l.opts.registry == nil {
		l.opts.registry = listRegistry()
	}

	if err := l.checkOptions(); err != nil {
		return nil, err
	}

	if err := l.checkScopeKeys(); err != nil {
		return nil, err
	}

	if err := l.checkPaths(); err != nil {
		return nil, err
	}

	if err := l.compileOrders(); err != nil {
		return nil, err
	}

	// Presence fields are not declared to the MongoDB schema, whose presence rules keep a null value unknown under every
	// negation: the listing contract makes null differ from every value, so only an ordering comparison stays unknown, which
	// presenceForm achieves with MongoDB's own null rules ([ListEngine.translate]).
	semantics, err := NewSchema(
		WithSchemaZeroWhenAbsent(schema.ZeroFields()),
		WithSchemaSizeZeroWhenAbsent(true), // size() is total: a null or absent Presence value measures 0
	)
	if err != nil {
		return nil, coreerrs.Wrap(errors.Join(listing.ErrInvalidSchema, err), "MongoDB filter schema")
	}

	l.presence = map[string]bool{}
	for _, name := range schema.PresenceFields() {
		l.presence[name] = true
	}

	l.arrays = schema.FilterPolicy().ArrayFields()

	// The schema's policy authorizes the public names; the translator binds them to the BSON paths.
	l.translator, err = NewFilterTranslatorWithSchema(schema.FilterPolicy(), semantics,
		WithFilterOptions(filter.WithFieldMapping(l.filterPaths())))
	if err != nil {
		return nil, coreerrs.Wrap(errors.Join(listing.ErrInvalidSchema, err), "filter translator")
	}

	return l, nil
}

// MustNewListEngine is [NewListEngine] for package-level declarations with static configuration, such as a planning engine
// (var widgets = mongo.MustNewListEngine[Widget](nil, nil, widgetSchema)): it returns the engine NewListEngine builds and panics
// on an invalid configuration. The panic value is an error prefixed with "mongo.MustNewListEngine: " that wraps the
// NewListEngine error, so errors.Is on the recovered value matches whatever NewListEngine
// returned ([listing.ErrInvalidSchema] for an invalid schema). Use NewListEngine where the configuration
// comes from input rather than code.
func MustNewListEngine[D any](coll *mongo.Collection, codec pagetoken.Signer, schema *listing.Schema, opts ...ListEngineOption) *ListEngine[D] {
	l, err := NewListEngine[D](coll, codec, schema, opts...)
	if err != nil {
		panic(fmt.Errorf("mongo.MustNewListEngine: %w", err))
	}

	return l
}

func invalid(format string, args ...any) error {
	return coreerrs.Wrapf(listing.ErrInvalidSchema, format, args...)
}

// fieldPath returns the BSON path a field is filtered on.
func (l *ListEngine[D]) fieldPath(name string) string {
	if p, ok := l.opts.fieldPaths[name]; ok {
		return p
	}

	return name
}

// sortPath returns the BSON path a field is sorted on.
func (l *ListEngine[D]) sortPath(name string) string {
	if p, ok := l.opts.sortPaths[name]; ok {
		return p
	}

	return l.fieldPath(name)
}

// filterPaths returns the filter mapping of every declared field.
func (l *ListEngine[D]) filterPaths() map[string]string {
	out := map[string]string{}
	for _, name := range l.schema.Fields() {
		out[name] = l.fieldPath(name)
	}

	return out
}

// Schema returns the schema the engine serves.
func (l *ListEngine[D]) Schema() *listing.Schema { return l.schema }

// List returns one page of req. subject names the list (the gRPC full method), scope is the base filter every page is
// restricted to (the parent, a kind) and is ANDed with the request filter. A scoped list ([WithListScopeKeys]) requires scope
// to pin every scope key by a scalar equality and the key of [WithListParentKey] to req.Parent; otherwise List fails with
// [ErrScope] before querying. scope holds only the BSON containers (bson.D, bson.E, bson.M, map[string]any, bson.A and unnamed
// slices of admitted values), raw BSON (bson.Raw, bson.RawArray, bson.RawValue, at most 16 MiB) and scalar leaves (booleans,
// numbers, strings, []byte, time.Time, the bson scalar types, decimal.Decimal, decimal.NullDecimal, the date types of the
// engine's registry and optional.Optional of a leaf); any other value — a struct, a pointer, another map, a value with its own
// BSON marshaling — fails with [ErrScope], as does a scope holding a server-side JavaScript operator or beyond the validation
// limits. The sort is the resolved order with the tie-breaker; the page token is bound to subject, the parent, the schema's
// contract ID, the effective BSON filter (scope included) and the sort.
//
// Errors: an invalid request field is an InvalidArgument core/errors.FieldError on pageSize, parent, orderBy, filter or
// pageToken; a query beyond the time cap wraps [ErrTimeout] (see [WithListMaxTime]) and one whose caller's context ended is that
// context's error, either hiding whatever FindPage returned behind apierror.Classify; a document that does not decode into D
// matches the [WithListCorruptRecord] category and the decode error; any other failure is wrapped with the collection name.
// [WithListErrorMapper] replaces the classification of query failures.
func (l *ListEngine[D]) List(ctx context.Context, subject string, req listing.Request, scope bson.D) (page.Page[D], error) {
	q, err := l.schema.Compile(ctx, req)
	if err != nil {
		return page.Page[D]{}, err
	}

	if err = l.checkScope(ctx, scope, req.Parent); err != nil {
		return page.Page[D]{}, err
	}

	user, err := l.translate(q.Filter)
	if err != nil {
		return page.Page[D]{}, err
	}

	// A cap, not a default: the shorter of the caller's deadline and maxTime wins.
	qctx, cancel := corecontext.WithMaxTimeout(ctx, l.opts.maxTime)
	defer cancel()

	res, err := keyset.FindPage[D](qctx, l.coll, l.codec, keyset.Query{
		Filter:             and(scope, user),
		Registry:           l.opts.registry,
		TimestampPrecision: l.opts.timestampPrecision,
		TiebreakPath:       l.opts.tiebreakPath,
		Sort:               l.orders[q.Order.String()].sort,
		Projection:         l.opts.projection,
		Limit:              int64(q.Size),
		After:              req.PageToken,
		Subject:            keys.Compose(subject, req.Parent, l.schema.ContractID()),
	})
	if err != nil {
		return page.Page[D]{}, l.mapError(ctx, qctx, err, user != nil)
	}

	return page.Page[D]{Items: res.Items, NextPageToken: res.Next}, nil
}
