// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"
	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskmerge"
)

func TestRound10LiteralStarImplied(t *testing.T) {
	for _, beside := range []bool{false, true} {
		request := &structpb.Struct{Fields: map[string]*structpb.Value{"*": structpb.NewStringValue("new")}}
		stored := &structpb.Struct{Fields: map[string]*structpb.Value{"*": structpb.NewStringValue("old"), "keep": structpb.NewBoolValue(true)}}
		want := proto.CloneOf(stored)

		want.Fields["*"] = structpb.NewStringValue("new")
		if beside {
			request.Fields["color"] = structpb.NewStringValue("blue")
			want.Fields["color"] = structpb.NewStringValue("blue")
		}

		for _, value := range []bool{false, true} {
			var req, cur, expected proto.Message = request, stored, want
			if value {
				req, cur, expected = structpb.NewStructValue(request), structpb.NewStructValue(stored), structpb.NewStructValue(want)
			}

			for _, dynamic := range []bool{false, true} {
				for _, entry := range []string{"ApplyMask", "Apply", "ValidateStrict", "FromSetFields", "ImpliedPaths", "Spec", "Complete", "DeferredSpec", "Patch", "DeferredPatch"} {
					t.Run(fmt.Sprintf("beside%t/value%t/dynamic%t/%s", beside, value, dynamic, entry), func(t *testing.T) {
						input, current := dynamicForm(t, req, dynamic), dynamicForm(t, cur, dynamic)
						before := proto.Clone(current)
						opts := append(dynamicOptions(), update.WithCurrent(current))

						var (
							mask fieldmask.FieldMask
							err  error
						)

						switch entry {
						case "ApplyMask":
							mask, err = update.ApplyMask(nil, input, opts...)
						case "Apply":
							err = update.Apply(nil, input, opts...)
							// Apply cannot return its synthesized mask through a nil map.
							mask = fieldmask.FromPaths("`*`")
							if beside {
								mask = fieldmask.FromPaths("`*`", "color")
							}
						case "ValidateStrict":
							mask, err = update.ValidateStrict(nil, input, opts...)
						case "FromSetFields":
							mask = update.FromSetFields(input, opts...)
						case "ImpliedPaths":
							mask = fieldmask.FromPaths(update.ImpliedPaths(input, opts...)...)
						case "Patch", "DeferredPatch":
							spec, e := update.NewSpec(input, dynamicOptions()...)
							if e != nil {
								t.Fatal(e)
							}

							ctx := t.Context()

							if entry == "DeferredPatch" {
								pending, e := update.Prepare(nil, input, dynamicOptions()...)
								if e != nil {
									t.Fatal(e)
								}

								ctx = update.ContextWithPlan(ctx, pending)
							}

							patch := update.Patch[proto.Message, proto.Message, *int64]{Field: "resource", Spec: spec,
								Precondition: update.Int64Precondition[proto.Message]{Version: func(proto.Message) int64 { return 1 }},
								Read:         func(context.Context) (proto.Message, error) { return proto.Clone(current), nil },
								ToProto:      func(m proto.Message) proto.Message { return m },
								Save: func(_ context.Context, m proto.Message, _ update.Plan, _ *int64) (proto.Message, error) {
									return m, nil
								},
							}

							result, e := patch.Run(ctx, input, "", nil)
							if e != nil {
								t.Fatal(e)
							}

							requireProtoEqual(t, before, current)
							requireProtoEqual(t, dynamicForm(t, expected, dynamic), result)

							return
						default:
							spec, e := update.NewSpec(input, dynamicOptions()...)
							if e != nil {
								t.Fatal(e)
							}

							if entry != "Spec" {
								pending, e := update.Prepare(nil, input, dynamicOptions()...)
								if e != nil {
									t.Fatal(e)
								}

								if entry == "Complete" {
									mask, err = pending.Complete(current)
									break
								}

								opts = append(opts, update.WithDeferredPlan(pending))
							}

							plan, e := spec.Plan(input, nil, opts...)
							mask, err = fieldmask.FromPaths(plan.Paths...), e
						}

						if err != nil {
							t.Fatal(err)
						}

						requireProtoEqual(t, before, current)

						if err := update.MergePaths(current, input, mask.ToPaths(), dynamicOptions()...); err != nil {
							t.Fatal(err)
						}

						requireProtoEqual(t, dynamicForm(t, expected, dynamic), current)
					})
				}
			}
		}
	}
}

func TestRound10LiteralJSONReadMask(t *testing.T) {
	object := &structpb.Struct{Fields: map[string]*structpb.Value{
		"*": structpb.NewStringValue("selected"), "`*`": structpb.NewStringValue("backticks"), "\x00*": structpb.NewStringValue("nul"), "keep": structpb.NewBoolValue(true),
	}}
	want := &structpb.Struct{Fields: map[string]*structpb.Value{"*": structpb.NewStringValue("selected")}}

	for _, dynamic := range []bool{false, true} {
		for _, prune := range []bool{false, true} {
			input := dynamicForm(t, object, dynamic)
			mask := fieldmask.FromPaths("`*`")
			expected := want

			var err error
			if prune {
				err = mask.Prune(input)
				expected = proto.CloneOf(object)
				delete(expected.Fields, "*")
			} else {
				err = mask.Filter(input)
			}

			if err != nil {
				t.Fatal(err)
			}

			requireProtoEqual(t, dynamicForm(t, expected, dynamic), input)
		}

		request := dynamicForm(t, want, dynamic)
		mask := fieldmask.FromSetFields[fieldmask.FieldMask](request)

		stored := dynamicForm(t, object, dynamic)
		if err := update.MergePaths(stored, request, mask.ToPaths()); err != nil {
			t.Fatal(err)
		}

		requireProtoEqual(t, dynamicForm(t, object, dynamic), stored)
	}
}

func TestRound10InvalidUTF8CannotSelectLiteralStar(t *testing.T) {
	request := &structpb.Struct{Fields: map[string]*structpb.Value{"*": structpb.NewStringValue("new")}}
	for _, path := range []string{"\xff*", "`\xff*`", "\xff"} {
		_, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{path}}, proto.Clone(request))
		if !errors.Is(err, fieldmask.ErrInvalidPath) {
			t.Fatalf("ApplyMask(%q) error=%v; want invalid path", path, err)
		}

		mask := fieldmask.FromPaths(path)
		if err := update.MergePaths(proto.Clone(request), request, mask.ToPaths()); !errors.Is(err, fieldmask.ErrInvalidPath) {
			t.Fatalf("MergePaths(%q) error=%v; want invalid path", mask.ToPaths(), err)
		}
	}
}

// The comparison's relevance decision uses the active resolver, including
// recursive schemas, protected parents and OUTPUT_ONLY precedence. Its visit
// callback must never be charged for an irrelevant branch.
func TestRound10ComparisonRelevance(t *testing.T) {
	md := deepCorpusDescriptor(t, true)
	request := deepCorpusMessage(md, 3, "new", "same")
	stored := deepCorpusMessage(md, 3, "old", "same")

	for _, tc := range []struct {
		name       string
		resolve    behavior.Resolve
		maxDepth   int
		wantVisits int
		wantPath   string
		exceeded   bool
	}{
		{"annotations", behavior.Shared, 1, 1, "", false},
		{"hidden annotations", func(protoreflect.FieldDescriptor) []annotations.FieldBehavior { return nil }, 1, 0, "", false},
		{"protected parent", func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "tree" {
				return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
			}

			return nil
		}, 1, 1, "tree", false},
		{"protected recursive leaf", func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "value" {
				return []annotations.FieldBehavior{annotations.FieldBehavior_IDENTIFIER}
			}

			return nil
		}, 4, 4, "tree.child.child.value", false},
		{"relevant depth remains bounded", func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "value" {
				return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
			}

			return nil
		}, 1, 3, "", true},
		{"output-only parent", func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "tree" {
				return []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY, annotations.FieldBehavior_IMMUTABLE}
			}

			if fd.Name() == "value" {
				return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
			}

			return nil
		}, 1, 0, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			visits, exceeded := 0, false
			visit := func(_ string, depth int) bool {
				visits++

				if depth > tc.maxDepth {
					exceeded = true
					return false
				}

				return true
			}

			changes, err := maskmerge.ProtectedChanges(request.ProtoReflect(), stored.ProtoReflect(), fieldmask.FromPaths("tree"), tc.resolve, "", visit)
			if err != nil || exceeded != tc.exceeded || visits != tc.wantVisits {
				t.Fatalf("error=%v exceeded=%t visits=%d; want exceeded=%t visits=%d", err, exceeded, visits, tc.exceeded, tc.wantVisits)
			}

			if tc.wantPath == "" && len(changes) != 0 || tc.wantPath != "" && (len(changes) != 1 || changes[0].Path != tc.wantPath) {
				t.Fatalf("changes=%v; want path %q", changes, tc.wantPath)
			}
		})
	}
}

func TestRound10IrrelevantDepth(t *testing.T) {
	md := deepCorpusDescriptor(t, true)

	for _, depth := range []int{2, fieldbehavior.DefaultMaxDepth + 3} {
		for _, limit := range []int{1, fieldbehavior.DefaultMaxDepth} {
			t.Run(fmt.Sprintf("depth%d/limit%d", depth, limit), func(t *testing.T) {
				request := deepCorpusMessage(md, depth, "new", "same")
				stored := deepCorpusMessage(md, depth, "old", "same")

				want, before := proto.Clone(request), proto.Clone(stored)
				if err := fieldbehavior.StripUpdate(request, fieldbehavior.WithCurrent(stored), fieldbehavior.WithMaxDepth(limit)); err != nil {
					t.Fatal(err)
				}

				requireProtoEqual(t, want, request)
				requireProtoEqual(t, before, stored)
			})
		}
	}
}
