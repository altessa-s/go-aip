// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis

// FieldType represents the RediSearch schema type for a field.
type FieldType int

const (
	// FieldTypeNumeric represents a NUMERIC field indexed with SORTABLE.
	FieldTypeNumeric FieldType = iota
	// FieldTypeTag represents a TAG field (exact match, pipe-separated values).
	FieldTypeTag
	// FieldTypeText represents a TEXT field (full-text search with prefix/infix).
	FieldTypeText
)
