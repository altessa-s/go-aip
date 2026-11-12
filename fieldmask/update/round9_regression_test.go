// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
)

func TestRound9DynamicResults(t *testing.T) {
	object := func(color string, keep bool) *structpb.Struct {
		s := &structpb.Struct{Fields: map[string]*structpb.Value{"color": structpb.NewStringValue(color)}}
		if keep {
			s.Fields["keep"] = structpb.NewBoolValue(true)
		}

		return s
	}

	blue, red, mixed := object("blue", false), object("red", true), object("blue", true)
	for _, tc := range []struct {
		name                 string
		req, stored, partial proto.Message
		paths                []string
	}{
		{"Struct", blue, red, mixed, []string{"color"}},
		{"ValueObject", structpb.NewStructValue(blue), structpb.NewStructValue(red), structpb.NewStructValue(mixed), []string{"color"}},
		{"ValueScalar", structpb.NewNumberValue(2), structpb.NewNumberValue(1), structpb.NewNumberValue(2), nil},
		{"ValueList", structpb.NewListValue(&structpb.ListValue{Values: []*structpb.Value{structpb.NewNumberValue(2)}}), structpb.NewListValue(&structpb.ListValue{Values: []*structpb.Value{structpb.NewNumberValue(1), structpb.NewNumberValue(3)}}), nil, nil},
		{"ListValue", &structpb.ListValue{Values: []*structpb.Value{structpb.NewNumberValue(2)}}, &structpb.ListValue{Values: []*structpb.Value{structpb.NewNumberValue(1), structpb.NewNumberValue(3)}}, nil, nil},
		{"EmptyStruct", &structpb.Struct{}, red, red, nil},
		{"EmptyValue", &structpb.Value{}, structpb.NewNumberValue(1), structpb.NewNumberValue(1), nil},
		{"EmptyList", &structpb.ListValue{}, &structpb.ListValue{Values: []*structpb.Value{structpb.NewNumberValue(1)}}, nil, nil},
		{"NestedStruct", &pb.UpdateRequest{ExtraData: blue}, &pb.UpdateRequest{ExtraData: red}, &pb.UpdateRequest{ExtraData: mixed}, []string{"extra_data.color"}},
		{"NestedValue", &pb.UpdateRequest{ValueData: structpb.NewStructValue(blue)}, &pb.UpdateRequest{ValueData: structpb.NewStructValue(red)}, &pb.UpdateRequest{ValueData: structpb.NewStructValue(mixed)}, []string{"value_data.color"}},
		{"NestedList", &pb.UpdateRequest{ListData: &structpb.ListValue{Values: []*structpb.Value{structpb.NewStructValue(blue)}}}, &pb.UpdateRequest{ListData: &structpb.ListValue{Values: []*structpb.Value{structpb.NewStructValue(red)}}}, &pb.UpdateRequest{ListData: &structpb.ListValue{Values: []*structpb.Value{structpb.NewStructValue(mixed)}}}, []string{"list_data.*.color"}},
	} {
		for _, dynamic := range []bool{false, true} {
			for _, mode := range []string{"explicit", "implied", "replace"} {
				if mode == "explicit" && tc.paths == nil {
					continue
				}

				t.Run(fmt.Sprintf("%s/dynamic%t/%s", tc.name, dynamic, mode), func(t *testing.T) {
					req, stored := dynamicForm(t, tc.req, dynamic), dynamicForm(t, tc.stored, dynamic)
					raw := &fieldmaskpb.FieldMask{Paths: tc.paths}

					expected := tc.partial
					if expected == nil {
						expected = tc.req
					}

					if mode == "replace" {
						raw.Paths = []string{"*"}
						expected = tc.req
					}

					if mode == "implied" {
						raw = nil

						if len(tc.name) >= 6 && tc.name[:6] == "Nested" {
							expected = tc.req
						}

						if tc.name == "EmptyList" {
							expected = tc.stored
						}
					}

					want := dynamicForm(t, expected, dynamic)
					opts := dynamicOptions()

					spec, err := update.NewSpec(req, opts...)
					if err != nil {
						t.Fatal(err)
					}

					direct, err := spec.Plan(req, raw, update.WithCurrent(stored))
					if err != nil {
						t.Fatal(err)
					}

					got := proto.Clone(stored)
					if err := update.MergePaths(got, req, direct.Paths, opts...); err != nil {
						t.Fatal(err)
					}

					requireProtoEqual(t, want, got)

					for _, entry := range []string{"ApplyMask", "Apply", "ValidateStrict", "Complete", "DeferredSpec", "Patch", "DeferredPatch"} {
						t.Run(entry, func(t *testing.T) {
							input := dynamicForm(t, tc.req, dynamic)
							paths := direct.Paths

							var (
								m fieldmask.FieldMask
								e error
							)

							switch entry {
							case "ApplyMask":
								m, e = update.ApplyMask(raw, input, append(opts, update.WithCurrent(stored))...)
							case "Apply":
								m = fieldmask.FromPaths(raw.GetPaths()...)
								if raw == nil {
									m = nil
								}

								e = update.Apply(m, input, append(opts, update.WithCurrent(stored))...)
								if raw == nil {
									m = fieldmask.FromPaths(paths...)
								}
							case "ValidateStrict":
								m = fieldmask.FromPaths(raw.GetPaths()...)
								if raw == nil {
									m = nil
								}

								m, e = update.ValidateStrict(m, input, append(opts, update.WithCurrent(stored))...)
							case "Complete", "DeferredSpec":
								pending, err := update.Prepare(raw, input, opts...)
								if err != nil {
									t.Fatal(err)
								}

								if entry == "Complete" {
									m, e = pending.Complete(stored)
								} else {
									plan, err := spec.Plan(input, raw, update.WithCurrent(stored), update.WithDeferredPlan(pending))
									m, e = fieldmask.FromPaths(plan.Paths...), err
								}
							default:
								ctx := t.Context()

								if entry == "DeferredPatch" {
									pending, err := update.Prepare(raw, input, opts...)
									if err != nil {
										t.Fatal(err)
									}

									ctx = update.ContextWithPlan(ctx, pending)
								}

								patch := update.Patch[proto.Message, proto.Message, *int64]{Field: "resource", Spec: spec,
									Precondition: update.Int64Precondition[proto.Message]{Version: func(proto.Message) int64 { return 1 }},
									Read:         func(context.Context) (proto.Message, error) { return proto.Clone(stored), nil }, ToProto: func(m proto.Message) proto.Message { return m },
									Save: func(_ context.Context, m proto.Message, _ update.Plan, _ *int64) (proto.Message, error) {
										return m, nil
									},
								}

								actual, err := patch.Run(ctx, input, "", raw)
								if err != nil {
									t.Fatal(err)
								}

								requireProtoEqual(t, want, actual)

								return
							}

							if e != nil {
								t.Fatal(e)
							}

							if !slices.Equal(m.ToPaths(), fieldmask.FromPaths(paths...).ToPaths()) {
								t.Fatalf("mask %v; want %v", m.ToPaths(), paths)
							}

							actual := proto.Clone(stored)
							if err := update.MergePaths(actual, input, m.ToPaths(), opts...); err != nil {
								t.Fatal(err)
							}

							requireProtoEqual(t, want, actual)
						})
					}
				})
			}
		}
	}
}

func TestRound9StrictDynamicReplacement(t *testing.T) {
	for _, input := range []proto.Message{&structpb.Struct{}, &structpb.ListValue{}, structpb.NewNumberValue(2)} {
		for _, dynamic := range []bool{false, true} {
			req := dynamicForm(t, input, dynamic)
			opts := []update.Option{update.WithStrictDynamicPaths(true)}

			mask, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"*"}}, req, opts...)
			if err != nil {
				t.Fatal(err)
			}

			stored := dynamicForm(t, input, dynamic)
			if err := update.MergePaths(stored, req, mask.ToPaths(), opts...); err != nil {
				t.Fatal(err)
			}

			if err := update.MergePaths(stored, req, []string{"*", "color"}, opts...); err == nil {
				t.Fatal("combined root wildcard accepted")
			}
		}
	}
}
