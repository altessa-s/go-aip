// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-atlas-core/encoding/keys"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

type pin struct {
	key   string
	value any
}

func (l *ListEngine[T]) canonicalScope(scope Scope, parent string) ([]pin, string, error) {
	out := make([]pin, 0, len(scope))
	keysIn := slices.Sorted(maps.Keys(scope))
	canonical := make([]string, 0, len(keysIn))

	allowed := map[string]bool{}
	for _, k := range l.opts.scopeKeys {
		allowed[k] = true
	}

	for _, k := range keysIn {
		if !allowed[k] {
			return nil, "", coreerrs.Wrapf(ErrScope, "undeclared scope key %q", k)
		}

		f, declared := l.schema.FieldInfo(k)
		v := scope[k]

		if declared {
			var err error

			v, err = normalizeScope(v, l.opts.kindResolver(f.Kind), l.opts.timestampPrecision)
			if err != nil {
				return nil, "", coreerrs.Wrapf(ErrScope, "key %q: %w", k, err)
			}
		} else {
			var err error

			v, err = normalizeScopeScalar(v, l.opts.timestampPrecision)
			if err != nil {
				return nil, "", coreerrs.Wrapf(ErrScope, "key %q: %w", k, err)
			}
		}

		out = append(out, pin{l.column(k), v})
		canonical = append(canonical, k+":"+fmt.Sprintf("%T:%v", v, v))
	}

	for _, k := range l.opts.scopeKeys {
		if _, ok := scope[k]; !ok {
			return nil, "", coreerrs.Wrapf(ErrScope, "scope key %q is not pinned", k)
		}
	}

	if l.opts.parentKey != "" {
		v, ok := scope[l.opts.parentKey].(string)
		if !ok || v != parent {
			return nil, "", coreerrs.Wrap(ErrScope, "parent mismatch")
		}
	}

	return out, keys.Compose(canonical...), nil
}

// normalizeScopeScalar gives private scope fields the same scalar representation as public fields.
func normalizeScopeScalar(v any, precision time.Duration) (any, error) {
	var kind filter.FieldKind

	switch v.(type) {
	case string:
		kind = filter.FieldKindString
	case bool:
		kind = filter.FieldKindBool
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		kind = filter.FieldKindInt
	case float64:
		kind = filter.FieldKindFloat
	case time.Time:
		kind = filter.FieldKindTimestamp
	case decimal.Decimal:
		kind = filter.FieldKindDecimal
	}

	return normalizeScope(v, DefaultSQLKind(kind), precision)
}

func normalizeScope(v any, k Kind, precision time.Duration) (any, error) {
	switch k {
	case Text, UUID:
		if x, ok := v.(string); ok {
			return x, nil
		}
	case Integer:
		switch x := v.(type) {
		case int:
			return int64(x), nil
		case int8:
			return int64(x), nil
		case int16:
			return int64(x), nil
		case int32:
			return int64(x), nil
		case int64:
			return x, nil
		case uint:
			return signedInteger(uint64(x))
		case uint8:
			return int64(x), nil
		case uint16:
			return int64(x), nil
		case uint32:
			return int64(x), nil
		case uint64:
			return signedInteger(x)
		}
	case Boolean:
		if x, ok := v.(bool); ok {
			return x, nil
		}
	case Float:
		if x, ok := v.(float64); ok {
			return x, nil
		}
	case Timestamp:
		if x, ok := v.(time.Time); ok {
			return x.UTC().Truncate(precision), nil
		}
	case Numeric:
		if x, ok := v.(decimal.Decimal); ok {
			return x.String(), nil
		}

		if x, ok := v.(string); ok {
			d, err := decimal.NewFromString(x)
			if err == nil {
				return d.String(), nil
			}
		}
	default:
		// No other kind is a valid scope value.
	}

	return nil, fmt.Errorf("value %T does not match field kind", v)
}

func signedInteger(v uint64) (int64, error) {
	if v > ^uint64(0)>>1 {
		return 0, fmt.Errorf("integer exceeds int64 range")
	}

	return int64(v), nil
}
