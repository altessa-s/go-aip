// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis

// mustSchema returns the RediSearch schema of fields and opts, panicking on an invalid declaration.
func mustSchema(fields map[string]FieldType, opts ...SchemaOption) *Schema {
	schema, err := NewSchema(fields, opts...)
	if err != nil {
		panic(err)
	}

	return schema
}
