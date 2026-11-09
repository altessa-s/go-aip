// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis

//go:generate optgen generate --type=schemaOptions --option-type=SchemaOption --output=schema_options_gen.go

type schemaOptions struct {
	// WithSchemaStrict rejects fields missing from the schema instead of falling back to TAG. It requires a non-empty field map:
	// [NewSchema] fails with [filter.ErrInvalidOptions] otherwise.
	strict bool `opt:"SchemaStrict" optval:"param"`
	// WithSchemaIndexMissing lists the storage fields declared INDEXMISSING in FT.CREATE (RediSearch 2.10+, DIALECT 2+). Only
	// these fields translate `has(f)`, `f == null` and `f != null`, through ismissing(@f); on any other field they fail with
	// filter.ErrUnsupportedOperation.
	indexMissing []string `opt:"SchemaIndexMissing" optgen:"allownull" optval:"clone,notrim,nodup"`
	// WithSchemaIndexEmpty lists the TAG and TEXT storage fields declared INDEXEMPTY in FT.CREATE (RediSearch 2.10+, DIALECT 2+).
	// Only these fields translate a comparison with the empty string, through @f:{""} (TAG) or @f:"" (TEXT); on any other field it
	// fails with filter.ErrUnsupportedOperation, since the empty tag query @f:{} is a syntax error.
	indexEmpty []string `opt:"SchemaIndexEmpty" optgen:"allownull" optval:"clone,notrim,nodup"`
}
