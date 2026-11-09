// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis

import (
	"github.com/altessa-s/go-aip/filter"

	coremaps "github.com/altessa-s/go-atlas-core/collections/maps"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Schema describes the RediSearch index (FT.CREATE) a [FilterTranslator] renders for: the index type of each storage field
// (after the field mapping) and the fields declared INDEXMISSING or INDEXEMPTY. It is immutable and safe to share between
// translators.
type Schema struct {
	fields       *coremaps.ImmutableMap[string, FieldType]
	strict       bool
	indexMissing map[string]struct{}
	indexEmpty   map[string]struct{}
}

// NewSchema returns the index description with fields mapping storage field names to their RediSearch types; the map is
// copied. Each comparison, membership test and string function renders in the syntax of the field's type, and a field
// missing from fields is treated as TAG unless [WithSchemaStrict] rejects it. WithSchemaStrict(true) with an empty field map
// fails with [filter.ErrInvalidOptions], since it would reject every field.
func NewSchema(fields map[string]FieldType, opts ...SchemaOption) (*Schema, error) {
	o := newSchemaOptions(opts...)
	if o.strict && len(fields) == 0 {
		return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "WithSchemaStrict requires a non-empty field map")
	}

	return &Schema{fields: coremaps.NewImmutableMap(fields), strict: o.strict,
		indexMissing: fieldSet(o.indexMissing), indexEmpty: fieldSet(o.indexEmpty)}, nil
}

// defaultSchema is the schema of [NewFilterTranslator]: every field is TAG and none is INDEXMISSING or INDEXEMPTY.
func defaultSchema() *Schema {
	return &Schema{fields: coremaps.NewImmutableMap[string, FieldType](nil)}
}

// fieldSet indexes a list of storage field names.
func fieldSet(fields []string) map[string]struct{} {
	set := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		set[field] = struct{}{}
	}

	return set
}
