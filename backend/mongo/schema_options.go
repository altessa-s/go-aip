// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"slices"

	"github.com/altessa-s/go-aip/filter"
)

//go:generate optgen generate --type=schemaOptions --option-type=SchemaOption --output=schema_options_gen.go

type schemaOptions struct {
	// Set by WithSchemaZeroWhenAbsent: it filters unspecified kinds before storing the map.
	zeroWhenAbsent map[string]filter.FieldKind `optfn:"SchemaZeroWhenAbsent=copyKinds" optgen:"default=make(map[string]filter.FieldKind)"`
	// Set by WithSchemaOmitEmpty: it preserves nil for an empty variadic call.
	omitEmpty          []string `optfn:"SchemaOmitEmpty=copyFields" optgen:"default=make([]string, 0)"`
	sizeZeroWhenAbsent bool     `opt:"SchemaSizeZeroWhenAbsent" optgen:"default=false" optval:"param"`
	// Set by WithSchemaPresenceFields: it preserves nil for an empty variadic call.
	presenceFields []string `optfn:"SchemaPresenceFields=copyFields" optgen:"default=make([]string, 0)"`
}

func copyFields(fields ...string) []string { return slices.Clone(fields) }

func copyKinds(fields map[string]filter.FieldKind) map[string]filter.FieldKind {
	kinds := make(map[string]filter.FieldKind, len(fields))
	for name, kind := range fields {
		if kind != filter.FieldKindUnspecified {
			kinds[name] = kind
		}
	}

	return kinds
}
