// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing_test

import (
	"testing"

	"github.com/altessa-s/go-aip/listing"
)

func BenchmarkCompile(b *testing.B) {
	s := widgetSchema(b, listing.WithDefaultEqualities(map[string]any{"archived": false}))

	req := listing.Request{PageSize: 50, Filter: `kind in ["KIND_A", "KIND_B"] && priority > 1 && name.startsWith("n")`, OrderBy: "kind, priority desc"}
	for b.Loop() {
		if _, err := s.Compile(b.Context(), req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompileReadMask(b *testing.B) {
	s := widgetSchema(b)

	req := listing.Request{PageSize: 50, OrderBy: "kind, priority desc", ReadMask: []string{"id", "name", "kind", "priority", "createTime"}}
	for b.Loop() {
		if _, err := s.Compile(b.Context(), req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCompileUnrestrictedOrder exercises dynamically resolved sort keys.
func BenchmarkCompileUnrestrictedOrder(b *testing.B) {
	s := widgetSchema(b, listing.WithUnrestrictedOrder(true))

	req := listing.Request{OrderBy: "name, priority desc"}
	for b.Loop() {
		if _, err := s.Compile(b.Context(), req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSchemaFieldEnum reads the declaration of an enum field through the public, detached-copy accessor.
func BenchmarkSchemaFieldEnum(b *testing.B) {
	s := widgetSchema(b)
	for b.Loop() {
		if _, ok := s.Field("kind"); !ok {
			b.Fatal("kind is not declared")
		}
	}
}

// BenchmarkSchemaFieldInfoEnum reads the declaration of an enum field through the zero-copy accessor.
func BenchmarkSchemaFieldInfoEnum(b *testing.B) {
	s := widgetSchema(b)
	for b.Loop() {
		if _, ok := s.FieldInfo("kind"); !ok {
			b.Fatal("kind is not declared")
		}
	}
}
