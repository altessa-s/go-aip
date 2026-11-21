// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-mongox/bsonfilter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// checkScope verifies that scope holds only the values a filter admits (see [ListEngine.List]), pins every scope key of the
// engine by a top-level equality on a scalar and binds the parent key to parent: a store that omits or mistypes its parent pin
// fails instead of reading the items of every parent. A pin is judged by its BSON encoding, as the driver sends it, so a
// document, an array or null is never a pin, whatever its Go type. The context ending stops the check with its error, unwrapped.
func (l *ListEngine[D]) checkScope(ctx context.Context, scope bson.D, parent string) error {
	if err := bsonfilter.Validate(ctx, scope, l.opts.registry); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
			return err
		}

		return fmt.Errorf("%w: %w", ErrScope, err)
	}

	for _, key := range l.opts.scopeKeys {
		i := slices.IndexFunc(scope, func(e bson.E) bool { return e.Key == key })
		if i < 0 {
			return coreerrs.Wrapf(ErrScope, "scope key %q is not pinned", key)
		}

		v, err := l.scopeValue(scope[i].Value)
		if err != nil {
			return fmt.Errorf("%w: scope key %q: %w", ErrScope, key, err)
		}

		if key == l.opts.parentKey {
			if str, ok := v.StringValueOK(); !ok || str != parent {
				return coreerrs.Wrapf(ErrScope, "scope key %q is not pinned to the parent", key)
			}
		}
	}

	return nil
}

// scalarPins are the BSON types an equality pin may have: an operator document, an array (any element matches) and null (absent
// matches) are not pins.
var scalarPins = []bson.Type{
	bson.TypeString, bson.TypeInt32, bson.TypeInt64, bson.TypeDouble, bson.TypeDecimal128,
	bson.TypeBoolean, bson.TypeDateTime, bson.TypeObjectID,
}

// errNotAPin reports a scope value whose BSON type is not a scalar pin.
var errNotAPin = errors.New("not an equality pin")

// scopeValue encodes v, a value [bsonfilter.Validate] admitted, as the driver would, with the engine's registry, and returns it
// when it is a scalar pin. The encoding is bounded, for a codec of the registry that expands.
func (l *ListEngine[D]) scopeValue(v any) (bson.RawValue, error) {
	raw, err := marshal(bson.D{{Key: "v", Value: v}}, l.opts.registry)
	if err != nil {
		return bson.RawValue{}, fmt.Errorf("the pin does not encode: %w", err)
	}

	rv := raw.Lookup("v")
	if !slices.Contains(scalarPins, rv.Type) {
		return bson.RawValue{}, fmt.Errorf("%w: a %s", errNotAPin, rv.Type)
	}

	return rv, nil
}
