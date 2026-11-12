// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package aipmap_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"
)

func readGeneratedRefusals(t *testing.T) []generatedRefusalRecord {
	t.Helper()

	packed, err := os.ReadFile("testdata/refusals_generated_base.jsonl.gz")
	if err != nil {
		t.Fatal(err)
	}

	const checksum = "6d629094ae5833c5838afd1205182bffe3e57a9907e88a21c172f5985788d0fd"
	if got := fmt.Sprintf("%x", sha256.Sum256(packed)); got != checksum {
		t.Fatalf("generated base corpus checksum = %s, want %s", got, checksum)
	}

	reader, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	decoder := json.NewDecoder(reader)

	var records []generatedRefusalRecord

	for {
		var r generatedRefusalRecord
		if err := decoder.Decode(&r); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			t.Fatal(err)
		}

		records = append(records, r)
	}

	return records
}

// Every generated input asserts the base's entire aggregate prefix and exact
// first Spec/Patch error, directly and through a deferred plan. Newly diagnosed
// fields have a literal order written by the independent generator's rule.
// No case, path or selection mode is exempt from parity or determinism checks.
func TestGeneratedRefusalBaseParity(t *testing.T) {
	cases, records := generatedRefusalCases(), readGeneratedRefusals(t)
	if len(cases) != len(records) {
		t.Fatalf("generated records = %d, want %d", len(records), len(cases))
	}

	for i, c := range cases {
		r := records[i]

		t.Run(fmt.Sprintf("%03d/%s/%s", c.ID, c.Layout, c.Mode), func(t *testing.T) {
			req, stored, raw, opts := generatedRefusalFixture(c)
			if !reflect.DeepEqual(c, r.Case) || generatedRefusalWire(req) != r.Request || generatedRefusalWire(stored) != r.Stored {
				t.Fatal("seeded inputs differ from the base recording")
			}

			if c.Layout == "indexed-list" {
				assertGeneratedIndexRejection(t, req, stored, raw, opts, r)
				return
			}

			if len(r.Order) != len(c.Changed) || !slices.Equal(r.Order, generatedRefusalOrder(c, r.Apply)) {
				t.Fatalf("golden order must include every changed field: %v", r.Order)
			}

			_, direct := update.ApplyMask(raw, proto.Clone(req), append(slices.Clone(opts), update.WithCurrent(stored))...)
			assertGeneratedAggregate(t, direct, r)

			for range 2 {
				_, repeated := update.ApplyMask(raw, proto.Clone(req), append(slices.Clone(opts), update.WithCurrent(stored))...)
				assertGeneratedAggregate(t, repeated, r)
			}

			_, prepared := update.Prepare(raw, proto.Clone(req), append(slices.Clone(opts), update.WithCurrent(stored))...)
			assertGeneratedAggregate(t, prepared, r)

			spec, err := update.NewSpec(req, opts...)
			if err != nil {
				t.Fatal(err)
			}

			_, directSpec := spec.Plan(proto.Clone(req), raw, update.WithCurrent(stored))
			assertGeneratedPlan(t, directSpec, generatedConcretePlan(r.Spec, r), r.Order[0])

			patch := refusalPatch(spec, stored)
			_, directPatch := patch.Run(t.Context(), proto.Clone(req), "", raw)
			assertGeneratedPlan(t, directPatch, generatedConcretePlan(r.Patch, r), r.Order[0])

			pending, err := update.Prepare(raw, req, opts...)

			if c.Layout == "required" {
				// REQUIRED is independent of stored state: Prepare must refuse
				// before it can return a plan. Check that refusal against the
				// base, rather than exempting these inputs from parity.
				var violation *update.BehaviorError
				if pending != nil || !errors.As(err, &violation) || !slices.Equal(violation.Violations, r.Apply) {
					t.Fatalf("stateless REQUIRED preparation = %v, want base %v", err, r.Apply)
				}

				if generatedRefusalWire(stored) != r.Stored {
					t.Fatal("comparison mutated the stored resource")
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			for range 2 {
				_, complete := pending.Complete(stored)
				assertGeneratedAggregate(t, complete, r)
			}

			ctx := update.ContextWithPlan(t.Context(), pending)
			_, deferredSpec := spec.Plan(req, pending.Mask().ToProtoFieldMask(), update.WithCurrent(stored), update.WithPlanContext(ctx))
			assertGeneratedPlan(t, deferredSpec, generatedConcretePlan(r.Spec, r), r.Order[0])

			if !reflect.DeepEqual(refusalTree(directSpec), refusalTree(deferredSpec)) {
				t.Fatal("direct and deferred Spec errors differ")
			}

			_, deferredPatch := patch.Run(ctx, req, "", pending.Mask().ToProtoFieldMask())
			assertGeneratedPlan(t, deferredPatch, generatedConcretePlan(r.Patch, r), r.Order[0])

			if !reflect.DeepEqual(refusalTree(directPatch), refusalTree(deferredPatch)) {
				t.Fatal("direct and deferred Patch errors differ")
			}

			if generatedRefusalWire(stored) != r.Stored {
				t.Fatal("comparison mutated the stored resource")
			}
		})
	}
}

func assertGeneratedAggregate(t *testing.T, err error, r generatedRefusalRecord) {
	t.Helper()

	var behaviorError *update.BehaviorError
	if !errors.As(err, &behaviorError) {
		t.Fatalf("expected BehaviorError, got %v", err)
	}

	violations := behaviorError.Violations
	if len(violations) != len(r.Order) {
		t.Fatalf("violations = %v, want order %v", violations, r.Order)
	}

	prefix := generatedRefusalPrefix(r.Case, r.Apply)
	if len(prefix) > len(violations) || !slices.Equal(violations[:len(prefix)], prefix) {
		t.Fatalf("aggregate reordered a base field: got %v, base %v", violations, r.Apply)
	}

	for i, v := range violations {
		behavior, reason := annotations.FieldBehavior_IMMUTABLE, "immutable field cannot be changed from its stored value"
		if r.Case.Layout == "required" && v.Path == generatedRefusalPaths(r.Case)[1] {
			behavior, reason = annotations.FieldBehavior_REQUIRED, "required field must be set to a non-empty value"
		}

		if v.Path != r.Order[i] || v.Behavior != behavior || v.Reason != reason {
			t.Fatalf("violation %d = %#v, want %s %s (%s)", i, v, behavior, r.Order[i], reason)
		}
	}
}

// The base reported collapsed wildcard fields. Replace only that resource
// field with the first selected concrete field; all wrappers, identities and
// original mask indexes/spellings must remain identical to the base recording.
func generatedConcretePlan(shape refusalShape, r generatedRefusalRecord) refusalShape {
	if len(r.Apply) == 0 || len(r.Order) == 0 || r.Apply[0].Path == r.Order[0] {
		return shape
	}

	if shape.Field == r.Apply[0].Path {
		shape.Field = r.Order[0]
	} else if shape.Field == "resource."+r.Apply[0].Path {
		shape.Field = "resource." + r.Order[0]
	}

	shape.Children = slices.Clone(shape.Children)
	for i, child := range shape.Children {
		shape.Children[i] = generatedConcretePlan(child, r)
	}

	return shape
}

func assertGeneratedPlan(t *testing.T, err error, base refusalShape, fallback string) {
	t.Helper()

	if base.Type != "" {
		if got := refusalTree(err); !reflect.DeepEqual(got, base) {
			t.Fatalf("first refusal must equal base exactly:\ngot %#v\nbase %#v", got, base)
		}

		return
	}

	var pathError *update.MaskPathError
	if !errors.As(err, &pathError) || !errors.Is(err, update.ErrProtectedField) || pathError.Field != fallback {
		t.Fatalf("base accepted; deterministic first field = %v, want %s", err, fallback)
	}
}

func assertGeneratedIndexRejection(t *testing.T, req, stored proto.Message, raw *fieldmaskpb.FieldMask, opts []update.Option, r generatedRefusalRecord) {
	t.Helper()

	if r.Rejection == nil || !slices.Contains(r.Rejection.Identities, "invalid-path") || len(r.Order) != 0 {
		t.Fatal("base must reject list indexes before behavior checking")
	}

	_, err := update.ApplyMask(raw, proto.Clone(req), append(slices.Clone(opts), update.WithCurrent(stored))...)
	if !errors.Is(err, fieldmask.ErrInvalidPath) || !reflect.DeepEqual(refusalTree(err), *r.Rejection) {
		t.Fatalf("indexed-list ApplyMask = %v, want base %#v", err, *r.Rejection)
	}

	for _, current := range []bool{false, true} {
		preparation := slices.Clone(opts)
		if current {
			preparation = append(preparation, update.WithCurrent(stored))
		}

		pending, err := update.Prepare(raw, proto.Clone(req), preparation...)
		if pending != nil || !reflect.DeepEqual(refusalTree(err), *r.Rejection) {
			t.Fatalf("indexed-list Prepare(current=%t) = %v, want base %#v", current, err, *r.Rejection)
		}
	}

	spec, err := update.NewSpec(req, opts...)
	if err != nil {
		t.Fatal(err)
	}

	_, err = spec.Plan(proto.Clone(req), raw, update.WithCurrent(stored))
	if !reflect.DeepEqual(refusalTree(err), r.Spec) {
		t.Fatalf("indexed-list Spec = %v, want base %#v", err, r.Spec)
	}

	patch := refusalPatch(spec, stored)

	_, err = patch.Run(t.Context(), proto.Clone(req), "", raw)
	if !reflect.DeepEqual(refusalTree(err), r.Patch) {
		t.Fatalf("indexed-list Patch = %v, want base %#v", err, r.Patch)
	}

	if generatedRefusalWire(stored) != r.Stored {
		t.Fatal("rejected selection mutated stored state")
	}
}
