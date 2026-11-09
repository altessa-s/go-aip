// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis_test

import "github.com/altessa-s/go-aip/backend/redis"

// mustSchema returns the RediSearch schema of fields and opts, panicking on an invalid declaration.
func mustSchema(fields map[string]redis.FieldType, opts ...redis.SchemaOption) *redis.Schema {
	schema, err := redis.NewSchema(fields, opts...)
	if err != nil {
		panic(err)
	}

	return schema
}
