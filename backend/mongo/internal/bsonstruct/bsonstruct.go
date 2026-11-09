// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package bsonstruct

import (
	"reflect"
	"strings"
)

// Field is how the driver's default struct codec reads one struct field.
type Field struct {
	// Encoded is set for a field the driver encodes: an exported one not tagged "-".
	Encoded bool
	// Key is the field's key: the name in its tag or its own name lowercased; empty for an inlined field.
	Key string
	// KeyBytes is the length of Key.
	KeyBytes int
	// Inlined is the struct type the driver describes in place of the field: the field's type, or the type it points to, for
	// an inline field of a struct or struct pointer type; nil otherwise (an inline map is not described).
	Inlined reflect.Type
}

// ReadField reads f as the driver's default struct tag parser does.
func ReadField(f reflect.StructField) Field {
	tag := driverTag(f)
	if !f.IsExported() || tag == "-" {
		return Field{}
	}

	key, inline := fieldKey(f.Name, tag)
	field := Field{Encoded: true, Key: key, KeyBytes: len(key)}

	if !inline {
		return field
	}

	ft := f.Type
	if ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}

	if ft.Kind() == reflect.Struct {
		field.Inlined = ft
	}

	return field
}

// fieldKey returns the key the driver's default struct tag parser gives the field name tagged tag, the name in its tag or its own
// name lowercased, and whether an "inline" option, or name, inlines it, which leaves it no key of its own.
func fieldKey(name, tag string) (key string, inline bool) {
	for i := 0; ; i++ {
		part, rest, more := strings.Cut(tag, ",")
		if part == "inline" {
			return "", true
		}

		if i == 0 && part != "" {
			key = part
		}

		if !more {
			break
		}

		tag = rest
	}

	if key == "" {
		key = strings.ToLower(name)
	}

	return key, false
}

// driverTag returns the struct tag the driver's default parser reads for f: its bson tag, or the whole tag when it has no key.
func driverTag(f reflect.StructField) string {
	tag, ok := f.Tag.Lookup("bson")
	if !ok && !strings.Contains(string(f.Tag), ":") {
		tag = string(f.Tag)
	}

	return tag
}
