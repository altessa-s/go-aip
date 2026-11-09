// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"errors"
	"strings"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

// CursorValueCheck vets one decoded cursor value of key before a dialect binds it, for example a decimal against the
// configured [DecimalFormat] of a database that would otherwise round it. A non-nil error makes the token invalid.
type CursorValueCheck func(key ListOrderKey, value any) error

// DecodeCursor decodes the sort values of pos into the bind values of keys, the last of which is the identity, as
// [ListSyntax.CursorPredicate] receives them. A position that does not match keys, or a value that does not match the
// storage kind of its key, fails with [pagetoken.ErrInvalidToken]. A non-nil check runs on each value as it is decoded;
// its error is joined with [pagetoken.ErrInvalidToken].
func DecodeCursor(keys []ListOrderKey, pos pagetoken.Position, check CursorValueCheck) ([]any, error) {
	if len(keys) == 0 || len(pos.Keys) != len(keys)-1 {
		return nil, pagetoken.ErrInvalidToken
	}

	vals := make([]any, 0, len(keys))

	for i, key := range keys {
		sv := pos.ID
		if i < len(pos.Keys) {
			sv = pos.Keys[i]
		}

		v, err := decodeSortValue(sv, key.StorageKind)
		if err != nil {
			return nil, err
		}

		if check != nil {
			if err := check(key, v); err != nil {
				return nil, errors.Join(pagetoken.ErrInvalidToken, err)
			}
		}

		vals = append(vals, v)
	}

	return vals, nil
}

// ExpandKeysetPredicate renders the lexicographic continuation predicate of keys as nested comparisons, for a database
// without row-value comparison or for keys of mixed directions:
//
//	((c1 > m1 OR (c1 = m1 AND (c2 > m2 OR (c2 = m2 AND c3 < m3)))))
//
// columns are the qualified, quoted columns of keys, marks their placeholders (such as "?" or a typed cast of it) and
// values their bind values, one entry per key. Each mark but the last is bound twice, so the returned arguments repeat
// its value. Without keys the predicate is empty.
func ExpandKeysetPredicate(keys []ListOrderKey, columns, marks []string, values []any) (string, []any) {
	if len(keys) == 0 {
		return "", nil
	}

	var (
		b    strings.Builder
		args = make([]any, 0, 2*len(keys)-1)
	)

	b.WriteByte('(')

	for i, key := range keys {
		op := " > "
		if key.Desc {
			op = " < "
		}

		if i == len(keys)-1 {
			b.WriteString(columns[i] + op + marks[i])
			args = append(args, values[i])

			break
		}

		b.WriteString("(" + columns[i] + op + marks[i] + " OR (" + columns[i] + " = " + marks[i] + " AND ")
		args = append(args, values[i], values[i])
	}

	b.WriteString(strings.Repeat("))", len(keys)-1))
	b.WriteByte(')')

	return b.String(), args
}
