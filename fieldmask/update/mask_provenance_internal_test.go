// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"errors"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldbehavior"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestMaskProvenancePreservesResourceWrappers(t *testing.T) {
	cause := &changedError{path: "owner.email"}
	mp := &MaskPathError{Index: 0, Path: "owner.email", Field: "owner.email", Err: cause, reason: "cannot change"}
	mask := &coreerrs.FieldError{Field: "request.mask.paths[0]", Reason: "cannot change", Err: mp}
	resource := &coreerrs.FieldError{Field: "resource.owner.email", Reason: "cannot change", Err: mask}
	origins := maskProvenance{{index: 2, path: "owner.*"}}
	translated := origins.translate(resource, "request.mask")

	outer, ok := translated.(*coreerrs.FieldError)
	if !ok || outer.Field != resource.Field || outer.Reason != resource.Reason {
		t.Fatalf("resource attribution changed: %v", translated)
	}

	inner, ok := outer.Err.(*coreerrs.FieldError)
	if !ok || inner.Field != "request.mask.paths[2]" {
		t.Fatalf("mask attribution not translated: %v", outer.Err)
	}

	got, ok := errors.AsType[*MaskPathError](translated)
	if !ok || got.Index != 2 || got.Path != "owner.*" || got.Field != mp.Field || got.Err != cause || got.reason != mp.reason {
		t.Fatalf("mask provenance or cause changed: %#v", got)
	}

	if !errors.Is(translated, ErrProtectedField) || !errors.Is(translated, cause) || mp.Index != 0 || mask.Field != "request.mask.paths[0]" {
		t.Fatal("translation changed classification or mutated the original error")
	}

	identity := originalMaskProvenance(&fieldmaskpb.FieldMask{Paths: []string{"ignored", "other", "owner.*"}})
	if identity.translate(translated, "request.mask") != translated {
		t.Fatal("Patch's identity translation must leave an already translated error intact")
	}

	wide := indexedFieldError(ErrBehaviorViolation, -1, "", "", "mask-wide refusal")
	if origins.translate(wide, DefaultUpdateMaskField) != wide || origins.translate(ErrMessageMismatch, DefaultUpdateMaskField) != ErrMessageMismatch {
		t.Fatal("unlocated errors must remain unchanged")
	}
}

// K0 cannot be bounded by the raw entry count while preserving keyed wildcard
// exclusions. Its provenance table must instead cover every generated entry.
func TestSanitizedMaskExpansionOrigins(t *testing.T) {
	request := &pb.Account{Delegates: map[string]*pb.Owner{
		"k": {Note: "ignored"}, "a": {Email: "a"}, "b": {Email: "b"}, "c": {Email: "c"},
	}}
	raw := &fieldmaskpb.FieldMask{Paths: []string{"delegates.k.note", "`delegates`.*.email"}}
	resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if fd.Name() == "note" {
			return []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY}
		}

		return nil
	})

	pending, err := Prepare(raw, request, WithResolver(resolver))
	if err != nil {
		t.Fatal(err)
	}

	input, err := pending.boundaryInput()
	if err != nil {
		t.Fatal(err)
	}

	_, sanitized, origins, err := pending.specInput(input)
	if err != nil {
		t.Fatal(err)
	}

	if len(sanitized.GetPaths()) != 3 || len(origins) != len(sanitized.GetPaths()) {
		t.Fatalf("sanitized mask=%v origins=%v, want three entries from two raw paths", sanitized, origins)
	}

	for i, path := range []string{"delegates.a.email", "delegates.b.email", "delegates.c.email"} {
		if sanitized.GetPaths()[i] != path || origins[i].index != 1 || origins[i].path != raw.GetPaths()[1] {
			t.Fatalf("entry %d: path=%q origin=%v", i, sanitized.GetPaths()[i], origins[i])
		}
	}
}
