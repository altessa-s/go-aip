// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/altessa-s/go-aip/fieldmask/update"
)

func TestPrepareSanitizesEmptySelections(t *testing.T) {
	policy := sourcePolicy{
		identity: "dependency", immutable: []string{"name"}, secret: "syntax", ignored: "edition",
	}
	for _, dynamic := range []bool{false, true} {
		for _, tc := range []struct {
			name      string
			paths     []string
			changed   bool
			secret    bool
			write     bool
			noMutable bool
		}{
			{name: "explicit empty output", paths: []string{}},
			{name: "explicit empty mutable and secret", paths: []string{}, secret: true, changed: true},
			{name: "output only", paths: []string{"edition"}},
			{name: "equal immutable", paths: []string{"name"}},
			{name: "changed immutable", paths: []string{"name"}, changed: true},
			{name: "omitted", write: true},
			{name: "omitted without mutable leaves", noMutable: true},
			{name: "selected mutable", paths: []string{"package"}, write: true},
			{name: "replacement", paths: []string{"*"}, write: true},
		} {
			t.Run(tc.name+map[bool]string{false: "/generated", true: "/dynamic"}[dynamic], func(t *testing.T) {
				input := &descriptorpb.FileDescriptorProto{
					Name: new("fixed"), Package: new("not a domain"), Edition: new(descriptorpb.Edition(999)),
					Dependency: []string{"routing"},
				}
				if tc.noMutable {
					input.Package = nil
				}

				if tc.changed {
					input.Name = new("changed")
				}

				if tc.secret {
					input.Syntax = new("secret")
				}

				convert := func(m proto.Message) proto.Message {
					if !dynamic {
						return proto.Clone(m)
					}

					out := dynamicpb.NewMessage(m.ProtoReflect().Descriptor())
					proto.Merge(out, m)

					return out
				}
				original := convert(input)
				req := proto.Clone(original)
				stored := convert(&descriptorpb.FileDescriptorProto{
					Name: new("fixed"), Package: new("stored.example"), Dependency: []string{"routing"},
				})

				opts := append(policy.options(), update.WithPathValidation(update.PathValidationDisabled))
				raw := maskOf(tc.paths)

				pending, err := update.Prepare(raw, req, opts...)
				if err != nil {
					t.Fatal(err)
				}

				want := proto.CloneOf(input)

				want.Edition, want.Syntax = nil, nil
				if !tc.write {
					want.Package = nil
				}

				requireProtoEqual(t, convert(want), req)

				// The stateless API remains an independent compatibility oracle for
				// effective paths; only Prepare's handler-visible filtering changes.
				baseline := proto.Clone(original)

				mask, err := update.ApplyMask(raw, baseline, opts...)
				if err != nil {
					t.Fatal(err)
				}

				if raw != nil && len(raw.Paths) == 0 && pending.Mask() == nil {
					t.Fatal("explicit empty mask became omitted")
				}

				if !slices.Equal(mask.ToPaths(), pending.Mask().ToPaths()) {
					t.Fatalf("Mask = %v, want %v", pending.Mask(), mask)
				}

				ops := pending.Operations()

				compares := tc.paths == nil || slices.Contains(tc.paths, "name") || slices.Contains(tc.paths, "*")
				if (len(ops) > 0) != compares {
					t.Fatalf("Operations = %v, comparison wanted: %t", ops, compares)
				}

				for _, op := range ops {
					if op.Field != "name" || op.Behavior != annotations.FieldBehavior_IMMUTABLE || op.Intent != update.DeferredSet {
						t.Fatalf("unexpected operation: %+v", op)
					}
				}

				completed, completeErr := pending.Complete(stored)

				refused := tc.changed && compares
				if refused {
					if completeErr == nil || !strings.Contains(completeErr.Error(), "name") {
						t.Fatalf("Complete refusal = %v, want name", completeErr)
					}

					requireProtoEqual(t, original, req)
				} else {
					if completeErr != nil {
						t.Fatal(completeErr)
					}

					stateful := proto.Clone(original)

					expected, err := update.ApplyMask(raw, stateful, append(opts, update.WithCurrent(stored))...)
					if err != nil {
						t.Fatal(err)
					}

					if !slices.Equal(expected.ToPaths(), completed.ToPaths()) {
						t.Fatalf("Complete mask = %v, want %v", completed, expected)
					}

					requireProtoEqual(t, stateful, req)
				}

				spec, err := update.NewSpec(req, opts...)
				if err != nil {
					t.Fatal(err)
				}

				plan, planErr := spec.Plan(req, pending.Mask().ToProtoFieldMask(), update.WithDeferredPlan(pending), update.WithCurrent(stored))
				if !refused {
					if planErr != nil {
						t.Fatal(planErr)
					}

					if plan.Empty() == tc.write {
						t.Fatalf("Empty = %t, write wanted: %t", plan.Empty(), tc.write)
					}

					if plan.Secret("syntax", new("secret")) != nil {
						t.Fatal("unselected secret consumed")
					}
				} else if planErr == nil {
					t.Fatal("Spec accepted changed immutable")
				}

				writes := 0
				patch := sourcePatch(spec, stored, func(_ proto.Message, p update.Plan) {
					writes++

					if p.Secret("syntax", new("secret")) != nil {
						t.Fatal("Save consumed unselected secret")
					}
				})

				out, err := patch.Run(update.ContextWithPlan(t.Context(), pending), req, "", pending.Mask().ToProtoFieldMask())
				if refused {
					if err == nil || writes != 0 {
						t.Fatalf("Patch refusal = %v, writes = %d", err, writes)
					}

					return
				}

				if err != nil {
					t.Fatal(err)
				}

				expected := proto.Clone(stored)
				if tc.write {
					if err := update.MergePaths(expected, original, plan.Paths, opts...); err != nil {
						t.Fatal(err)
					}
				}

				requireProtoEqual(t, expected, out)

				if writes != map[bool]int{false: 0, true: 1}[tc.write] {
					t.Fatalf("writes = %d", writes)
				}
			})
		}
	}
}

func TestPrepareEmptyPreservesAtomicComparisonInput(t *testing.T) {
	policy := sourcePolicy{
		atomic: "google.protobuf.FileOptions",
		fields: map[protoreflect.FullName]annotations.FieldBehavior{
			"google.protobuf.FileOptions.java_package": annotations.FieldBehavior_IMMUTABLE,
			"google.protobuf.FileOptions.optimize_for": annotations.FieldBehavior_OUTPUT_ONLY,
		},
	}

	for _, paths := range [][]string{{}, nil} {
		req := &descriptorpb.FileDescriptorProto{
			Options: &descriptorpb.FileOptions{
				JavaPackage: new("fixed"), JavaOuterClassname: new("retained for atomic comparison"),
				OptimizeFor: new(descriptorpb.FileOptions_OptimizeMode(999)),
			},
		}
		original := proto.Clone(req)

		pending, err := update.Prepare(maskOf(paths), req, policy.options()...)
		if err != nil {
			t.Fatal(err)
		}

		if req.Options.GetJavaPackage() != "fixed" || req.Options.GetJavaOuterClassname() == "" || req.Options.OptimizeFor != nil {
			t.Fatalf("atomic input = %v", req)
		}

		if paths != nil && len(pending.Operations()) != 0 {
			t.Fatalf("no-op has operations: %v", pending.Operations())
		}

		if _, err := pending.Complete(original); err != nil {
			t.Fatal(err)
		}
	}
}
