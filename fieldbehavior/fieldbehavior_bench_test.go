// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldbehavior_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldbehavior"

	testpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func BenchmarkStripCreate(b *testing.B) {
	tpl := fullResource()

	b.ReportAllocs()

	for b.Loop() {
		r, ok := proto.Clone(tpl).(*testpb.Resource)
		if !ok {
			b.Fatalf("clone produced unexpected type %T", r)
		}

		if err := fieldbehavior.StripCreate(r); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStripUpdate(b *testing.B) {
	tpl := fullResource()

	b.ReportAllocs()

	for b.Loop() {
		r, ok := proto.Clone(tpl).(*testpb.Resource)
		if !ok {
			b.Fatalf("clone produced unexpected type %T", r)
		}

		if err := fieldbehavior.StripUpdate(r, fieldbehavior.WithCurrent(tpl)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStripResponse(b *testing.B) {
	tpl := fullResource()

	b.ReportAllocs()

	for b.Loop() {
		r, ok := proto.Clone(tpl).(*testpb.Resource)
		if !ok {
			b.Fatalf("clone produced unexpected type %T", r)
		}

		if err := fieldbehavior.StripResponse(r); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStripCreateStrict measures the dry-run path; mutation cost is
// replaced by violation slice growth.
func BenchmarkStripCreateStrict(b *testing.B) {
	tpl := fullResource()

	b.ReportAllocs()

	for b.Loop() {
		r, ok := proto.Clone(tpl).(*testpb.Resource)
		if !ok {
			b.Fatalf("clone produced unexpected type %T", r)
		}

		_ = fieldbehavior.StripCreate(r, fieldbehavior.WithStrict(true))
	}
}

// BenchmarkStripResponseUnannotated measures the fast path: the message type
// carries no relevant annotation anywhere, so the walk is skipped entirely.
func BenchmarkStripResponseUnannotated(b *testing.B) {
	msg := &fieldmaskpb.FieldMask{Paths: []string{"a", "b", "c"}}

	b.ReportAllocs()

	for b.Loop() {
		if err := fieldbehavior.StripResponse(msg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStripCreateEmpty isolates the descriptor walk for resources that
// have no fields to strip.
func BenchmarkStripCreateEmpty(b *testing.B) {
	tpl := &testpb.Resource{Name: "n", Description: "d"}

	b.ReportAllocs()

	for b.Loop() {
		r, ok := proto.Clone(tpl).(*testpb.Resource)
		if !ok {
			b.Fatalf("clone produced unexpected type %T", r)
		}

		if err := fieldbehavior.StripCreate(r); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCloneFullResource measures the proto.Clone every Strip benchmark
// above pays per iteration, so the cost of the walk itself is the difference.
func BenchmarkCloneFullResource(b *testing.B) {
	tpl := fullResource()

	b.ReportAllocs()

	for b.Loop() {
		if _, ok := proto.Clone(tpl).(*testpb.Resource); !ok {
			b.Fatal("clone produced unexpected type")
		}
	}
}
