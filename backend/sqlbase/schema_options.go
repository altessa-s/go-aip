// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import "github.com/altessa-s/go-aip/filter"

//go:generate optgen generate --type=schemaOptions --option-type=SchemaOption --output=schema_options_gen.go

type schemaOptions struct {
	// WithSchemaKindValidator validates logical field kinds against SQL storage kinds. It must be concurrency safe.
	kindValidator func(filter.FieldKind, Kind) bool `opt:"SchemaKindValidator" optgen:"default=defaultKindValidator"`

	// WithSchemaColumnValidator replaces SQL column-name validation. The consuming dialect must quote every accepted identifier.
	columnValidator func(string) error `opt:"SchemaColumnValidator" optgen:"default=validateColumn"`
}

func defaultKindValidator(logical filter.FieldKind, storage Kind) bool {
	return logical == commonKind(storage)
}
