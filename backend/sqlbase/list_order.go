// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

// ListOrderKey describes a physical cursor key, including the final unique identity.
type ListOrderKey struct {
	Column string
	// StorageKind is the resolved SQL representation of the cursor value.
	StorageKind Kind
	// Field is the declaration of the sort field without its enum values; the identity carries only its kind.
	Field listing.Field
	Desc  bool
}

func (l *ListEngine[T]) orderKeys(o listing.Order) []ListOrderKey {
	return orderKeys(l.schema, l.opts.idColumn, l.opts.idKind, l.opts.kindResolver, l.column, o)
}

func orderKeys(schema *listing.Schema, idColumn string, idKind filter.FieldKind, kind func(filter.FieldKind) Kind,
	column func(string) string, o listing.Order,
) []ListOrderKey {
	out := make([]ListOrderKey, 0, len(o.Keys())+1)

	publicKeys := o.Keys()
	for i, k := range publicKeys {
		if k.Nulls != 0 || k.Collation != "" {
			return nil
		}

		col := column(k.Field)
		if col == idColumn {
			if i != len(publicKeys)-1 {
				return nil
			}

			continue
		}

		f, _ := schema.FieldInfo(k.Field)
		out = append(out, ListOrderKey{Column: col, StorageKind: kind(f.Kind), Field: listing.Field{
			Name: f.Name, Kind: f.Kind, Array: f.Array, Optional: f.Optional, Presence: f.Presence, Hidden: f.Hidden, EnumZero: f.EnumZero,
		}, Desc: k.Desc})
	}

	desc := false
	if len(publicKeys) > 0 {
		desc = publicKeys[len(publicKeys)-1].Desc
	}

	out = append(out, ListOrderKey{Column: idColumn, StorageKind: kind(idKind), Field: listing.Field{Kind: idKind}, Desc: desc})

	return out
}
