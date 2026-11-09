// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"context"
	"maps"
	"slices"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/listing"
)

// listOrder is the MongoDB form of a [listing.Order].
type listOrder struct {
	sort  bson.D // the keys at their sort paths, then the tie-breaker
	index string
}

// indexSpec is the index backing one declared pattern.
type indexSpec struct {
	name string
	keys bson.D
}

// compileOrders resolves every accepted order to its sort and every declared pattern to its backing index.
func (l *ListEngine[D]) compileOrders() error {
	names, err := l.patternIndexNames()
	if err != nil {
		return err
	}

	used := map[string]string{}
	orders := l.schema.Orders()

	for i := 0; i < len(orders); i += 2 { // each declared pattern, then its reversal
		declared, reversed := orders[i], orders[i+1]

		sort, err := l.sortOf(declared)
		if err != nil {
			return err
		}

		reversedSort, err := l.sortOf(reversed)
		if err != nil {
			return err
		}

		ixKeys := l.indexKeys(sort)
		builtin := len(ixKeys) == 1 && ixKeys[0].Key == DefaultTiebreakPath
		name, named := names[declared.String()]

		switch {
		case builtin && named:
			return invalid("pattern %q is served by the built-in index %q: do not name it", declared, IDIndex)
		case builtin:
			name = IDIndex
		case name == IDIndex:
			return invalid("pattern %q: index name %q is reserved for the unscoped id-only order", declared, IDIndex)
		case !named:
			name = indexName(ixKeys)
		}

		if !builtin {
			if prev, dup := used[name]; dup {
				return invalid("patterns %q and %q: index name %q used twice", prev, declared, name)
			}

			used[name] = declared.String()
			l.indexes = append(l.indexes, indexSpec{name: name, keys: ixKeys})
		}

		l.orders[declared.String()] = listOrder{sort: sort, index: name}
		l.orders[reversed.String()] = listOrder{sort: reversedSort, index: name}
	}

	return nil
}

// patternIndexNames resolves [WithListIndexNames] to the canonical orderBy of each declared pattern.
func (l *ListEngine[D]) patternIndexNames() (map[string]string, error) {
	declaredOf := map[string]string{} // canonical order (pattern or reversal) -> canonical pattern
	orders := l.schema.Orders()

	for i := 0; i < len(orders); i += 2 {
		declaredOf[orders[i].String()] = orders[i].String()
		declaredOf[orders[i+1].String()] = orders[i].String()
	}

	out := map[string]string{}

	for _, key := range slices.Sorted(maps.Keys(l.opts.indexNames)) {
		name := l.opts.indexNames[key]

		o, err := l.schema.Order(context.Background(), key)
		if err != nil || key == "" {
			return nil, invalid("index name %q: %q is not a declared pattern", name, key)
		}

		pattern := declaredOf[o.String()]
		if _, dup := out[pattern]; dup {
			return nil, invalid("pattern %q is named twice", pattern)
		}

		if name == "" {
			return nil, invalid("pattern %q: empty index name", pattern)
		}

		out[pattern] = name
	}

	return out, nil
}

// sortOf returns the full sort of o: its keys at their sort paths, then the configured tie-breaker in the direction of the last
// key (unless that key is the id itself). It refuses an order on the id anywhere but last, twice on one path or on a scope key.
func (l *ListEngine[D]) sortOf(o listing.Order) (bson.D, error) {
	orderKeys := o.Keys()
	sort := make(bson.D, 0, len(orderKeys)+1)

	for i, k := range orderKeys {
		if k.Nulls != 0 || k.Collation != "" {
			return nil, invalid("null placement and collation are not supported")
		}

		p := l.sortPath(k.Field)

		switch {
		case p == l.opts.tiebreakPath && i != len(orderKeys)-1:
			return nil, invalid("pattern %q: the id may only be the last key", o)
		case slices.Contains(l.opts.scopeKeys, p):
			return nil, invalid("pattern %q: field %q sorts on a scope key", o, k.Field)
		case slices.ContainsFunc(sort, func(e bson.E) bool { return e.Key == p }):
			return nil, invalid("pattern %q: field %q sorts on the same path %q as another key", o, k.Field, p)
		}

		sort = append(sort, bson.E{Key: p, Value: direction(k.Desc)})
	}

	if len(sort) == 0 {
		sort = append(sort, bson.E{Key: l.opts.tiebreakPath, Value: int32(1)})
	}

	if last := sort[len(sort)-1]; last.Key != l.opts.tiebreakPath {
		sort = append(sort, bson.E{Key: l.opts.tiebreakPath, Value: last.Value})
	}

	return sort, nil
}

// indexKeys returns the keys of the index backing a pattern's sort: the scope keys ascending, then the full sort.
func (l *ListEngine[D]) indexKeys(sort bson.D) bson.D {
	out := make(bson.D, 0, len(l.opts.scopeKeys)+len(sort))
	for _, k := range l.opts.scopeKeys {
		out = append(out, bson.E{Key: k, Value: int32(1)})
	}

	return append(out, sort...)
}

// direction is the MongoDB sort direction.
func direction(desc bool) int32 {
	if desc {
		return -1
	}

	return 1
}

// Sort returns the full MongoDB sort of o, an order of the engine's schema: the keys mapped to their sort paths, then the
// configured tie-breaker in the direction of the last key (unless the last key is the id itself); nil for an order of another
// schema.
func (l *ListEngine[D]) Sort(o listing.Order) bson.D { return slices.Clone(l.orders[o.String()].sort) }

// Index returns the name of the index backing o, an order of the engine's schema; "" for an order of another schema.
func (l *ListEngine[D]) Index(o listing.Order) string { return l.orders[o.String()].index }
