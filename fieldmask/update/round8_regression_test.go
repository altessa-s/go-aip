// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

var round8Entries = []string{"ApplyMask", "Apply", "ValidateStrict", "Prepare", "Complete", "Spec", "Patch", "StripUpdate"}

func round8Entry(t *testing.T, entry string, req, stored proto.Message, raw *fieldmaskpb.FieldMask, known bool, opts ...update.Option) (fieldmask.FieldMask, error) {
	t.Helper()

	if known {
		opts = append(opts, update.WithCurrent(stored))
	}

	mask := fieldmask.FromPaths(raw.GetPaths()...)
	if raw == nil {
		mask = nil
	}

	switch entry {
	case "ApplyMask":
		return update.ApplyMask(raw, req, opts...)
	case "Apply":
		err := update.Apply(mask, req, opts...)
		return mask, err
	case "ValidateStrict":
		return update.ValidateStrict(mask, req, opts...)
	case "Prepare", "Complete":
		pending, err := update.Prepare(raw, req, opts...)
		if err != nil {
			return nil, err
		}

		if entry == "Complete" {
			return pending.Complete(stored)
		}

		return pending.Mask(), nil
	case "StripUpdate":
		var stripOpts []fieldbehavior.Option
		if known {
			stripOpts = append(stripOpts, fieldbehavior.WithCurrent(stored))
		}

		return fieldmask.FieldMask{}, fieldbehavior.StripUpdate(req, stripOpts...)
	}

	// A valid schema lets Plan and Patch themselves receive nil/invalid input.
	template := req
	if dm, ok := template.(*dynamicpb.Message); template == nil || (ok && dm == nil) {
		template = &emptypb.Empty{}
	} else {
		template = template.ProtoReflect().Type().New().Interface()
	}

	spec, err := update.NewSpec(template, opts...)
	if err != nil {
		t.Fatal(err)
	}

	if entry == "Spec" {
		plan, err := spec.Plan(req, raw, opts...)
		return fieldmask.FromPaths(plan.Paths...), err
	}

	mask = fieldmask.FieldMask{}
	patch := update.Patch[proto.Message, proto.Message, *int64]{
		Field: "resource", Spec: spec,
		Precondition: update.Int64Precondition[proto.Message]{Version: func(proto.Message) int64 { return 1 }},
		Read:         func(context.Context) (proto.Message, error) { return round8Clone(stored), nil },
		ToProto:      func(m proto.Message) proto.Message { return m },
		Save: func(_ context.Context, m proto.Message, plan update.Plan, _ *int64) (proto.Message, error) {
			mask = fieldmask.FromPaths(plan.Paths...)
			return m, nil
		},
	}

	got, err := patch.Run(t.Context(), req, "", raw)
	if err == nil && !nilcheck.IsNil(req) && !nilcheck.IsNil(stored) && proto.Equal(req, stored) {
		requireProtoEqual(t, stored, got)
	}

	return mask, err
}

// round8Clone preserves invalid inputs so the public entry point, rather than
// proto.Clone, is what the test exercises.
func round8Clone(m proto.Message) proto.Message {
	if nilcheck.IsNil(m) {
		return m
	}

	return proto.Clone(m)
}

// These expectations were also exercised on HEAD~1: explicit JSON keys worked
// statelessly, while stateful calls already panicked there. Invalid resources
// now consistently use the documented mismatch error; Apply(nil, nil) and
// StripUpdate(nil) keep their no-op contracts.
func TestRound8RootEntryPoints(t *testing.T) {
	s := &structpb.Struct{Fields: map[string]*structpb.Value{"color": structpb.NewStringValue("blue")}}
	for _, tc := range []struct {
		name    string
		req     proto.Message
		paths   []string
		want    []string
		wantErr error
	}{
		{"Struct", s, []string{"color"}, []string{"color"}, nil},
		// Dynamic roots select populated JSON keys, or a populated list whole.
		{"StructImplied", s, nil, []string{"color"}, nil},
		{"ValueImplied", structpb.NewStructValue(s), nil, []string{"color"}, nil},
		{"ListValueImplied", &structpb.ListValue{Values: []*structpb.Value{structpb.NewStructValue(s)}}, nil, []string{"*"}, nil},
		{"StructFieldNameKey", &structpb.Struct{Fields: map[string]*structpb.Value{"fields": structpb.NewStringValue("data")}},
			[]string{"fields"}, []string{"fields"}, nil},
		{"StructQuotedKey", &structpb.Struct{Fields: map[string]*structpb.Value{"a.b": structpb.NewStringValue("data")}},
			[]string{"`a.b`"}, []string{"`a.b`"}, nil},
		{"StructMissingKey", s, []string{"absent"}, []string{"absent"}, nil},
		{"Value", structpb.NewStructValue(s), []string{"color"}, []string{"color"}, nil},
		{"ValueFieldNameKey", structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{"struct_value": structpb.NewStringValue("data")}}),
			[]string{"struct_value"}, []string{"struct_value"}, nil},
		{"ScalarValue", structpb.NewStringValue("blue"), []string{"color"}, []string{"color"}, nil},
		{"ListValue", &structpb.ListValue{Values: []*structpb.Value{structpb.NewStructValue(s)}}, []string{"*.color"}, nil, fieldmask.ErrInvalidPath},
		{"ListValueIndex", &structpb.ListValue{}, []string{"0"}, nil, fieldmask.ErrInvalidPath},
		{"EmptyUnknown", &emptypb.Empty{}, []string{"color"}, nil, fieldmask.ErrInvalidPath},
		{"EmptyImplied", &emptypb.Empty{}, nil, nil, nil},
		{"EmptyReplacement", &emptypb.Empty{}, []string{"*"}, nil, nil},
		{"NilDynamic", (*dynamicpb.Message)(nil), []string{"color"}, nil, update.ErrMessageMismatch},
		{"nil", nil, []string{"color"}, nil, update.ErrMessageMismatch},
		{"InvalidStruct", (*structpb.Struct)(nil), []string{"color"}, nil, update.ErrMessageMismatch},
		{"InvalidValue", (*structpb.Value)(nil), []string{"color"}, nil, update.ErrMessageMismatch},
		{"InvalidListValue", (*structpb.ListValue)(nil), []string{"*.color"}, nil, update.ErrMessageMismatch},
		{"InvalidEmpty", (*emptypb.Empty)(nil), []string{"color"}, nil, update.ErrMessageMismatch},
		{"InvalidDynamic", dynamicpb.NewMessageType(s.ProtoReflect().Descriptor()).Zero().Interface(), []string{"color"}, nil, update.ErrMessageMismatch},
	} {
		for _, empty := range []bool{false, true} {
			for _, known := range []bool{false, true} {
				for _, entry := range round8Entries {
					t.Run(fmt.Sprintf("%s/empty=%t/current=%t/%s", tc.name, empty, known, entry), func(t *testing.T) {
						defer func() {
							if p := recover(); p != nil {
								t.Errorf("entry point panicked: %v", p)
							}
						}()

						req, stored := round8Clone(tc.req), round8Clone(tc.req)

						raw, want, wantErr := maskOf(tc.paths), tc.want, tc.wantErr
						if empty {
							raw, want = &fieldmaskpb.FieldMask{}, nil

							if !errors.Is(wantErr, update.ErrMessageMismatch) {
								wantErr = nil
							}
						}

						if entry == "Apply" && raw == nil {
							want = nil // a nil map cannot return the synthesized mask
						}

						if entry == "StripUpdate" {
							want, wantErr = nil, nil
							if tc.name == "InvalidDynamic" && known {
								wantErr = fieldbehavior.ErrCurrentMismatch
							}
						} else if entry == "Apply" && tc.req == nil {
							want, wantErr = raw.GetPaths(), nil
						}

						got, err := round8Entry(t, entry, req, stored, raw, known)
						if !errors.Is(err, wantErr) {
							t.Fatalf("mask=%v error=%v; want error %v", got.ToPaths(), err, wantErr)
						}

						if err == nil && !slices.Equal(got.ToPaths(), want) {
							t.Fatalf("mask=%v; want %v", got.ToPaths(), want)
						}

						if nilcheck.IsNil(tc.req) {
							if req != tc.req || stored != tc.req {
								t.Fatal("nil resource changed")
							}

							return
						}

						requireProtoEqual(t, tc.req, stored)

						if err != nil || entry == "ValidateStrict" || entry == "Spec" || entry == "Patch" || entry == "StripUpdate" || empty {
							requireProtoEqual(t, tc.req, req)
						} else if tc.name == "StructMissingKey" {
							requireProtoEqual(t, &structpb.Struct{}, req)
						} else {
							requireProtoEqual(t, tc.req, req)
						}
					})
				}
			}
		}
	}
}

// A covered path's count contract still needs stored state even when the
// complete selection replaces the collection whole.
func TestRound8CoveredCountsAtCompletion(t *testing.T) {
	mutable := update.WithResolver(fieldbehavior.ResolverFunc(func(protoreflect.FieldDescriptor) []annotations.FieldBehavior { return nil }))

	for _, size := range []int{1, 2} {
		for _, entry := range []string{"ApplyMask", "Complete", "Spec", "Patch"} {
			t.Run(fmt.Sprintf("size=%d/%s", size, entry), func(t *testing.T) {
				req := &pb.Resource{Aliases: []*pb.Profile{{DisplayName: "a"}}}
				stored := &pb.Resource{Aliases: slices.Repeat([]*pb.Profile{{DisplayName: "a"}}, size)}

				got, err := round8Entry(t, entry, req, stored, maskOf([]string{"aliases", "aliases.*.display_name"}), entry != "Complete", mutable)
				if size == 2 {
					if !errors.Is(err, update.ErrElementCountMismatch) {
						t.Fatalf("mask=%v err=%v; want element count mismatch", got, err)
					}

					return
				}

				if err != nil || !slices.Equal(got.ToPaths(), []string{"aliases"}) {
					t.Fatalf("mask=%v err=%v; want whole collection", got, err)
				}
			})
		}
	}
}

func TestRound8RootJSONMergePreconditions(t *testing.T) {
	object := func(n int) *structpb.Struct {
		values := make([]*structpb.Value, n)
		for i := range values {
			values[i] = structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{"color": structpb.NewStringValue("blue")}})
		}

		return &structpb.Struct{Fields: map[string]*structpb.Value{"items": structpb.NewListValue(&structpb.ListValue{Values: values})}}
	}

	for _, value := range []bool{false, true} {
		for _, entry := range round8Entries[:len(round8Entries)-1] {
			for _, known := range []bool{false, true} {
				t.Run(fmt.Sprintf("value=%t/current=%t/%s", value, known, entry), func(t *testing.T) {
					var req, stored proto.Message = object(2), object(1)
					if value {
						req, stored = structpb.NewStructValue(object(2)), structpb.NewStructValue(object(1))
					}

					got, err := round8Entry(t, entry, req, stored, maskOf([]string{"items.*.color"}), known)
					if known || entry == "Complete" || entry == "Patch" {
						if !errors.Is(err, update.ErrElementCountMismatch) {
							t.Fatalf("mask=%v err=%v; want element count mismatch", got, err)
						}
					} else if err != nil || !slices.Equal(got.ToPaths(), []string{"items.*.color"}) {
						t.Fatalf("mask=%v err=%v; want deferred JSON selection", got, err)
					}
				})
			}
		}
	}
}

// TestRound8SeededNoPanic exercises several thousand reproducible combinations
// of roots, masks and entry points. Exact semantics belong to the tables above;
// this test closes the panic class even for malformed and mismatched inputs.
func TestRound8SeededNoPanic(t *testing.T) {
	rng := rand.New(rand.NewPCG(203, 8))
	s := &structpb.Struct{Fields: map[string]*structpb.Value{
		"color": structpb.NewStringValue("blue"), "fields": structpb.NewStringValue("data"),
		"a.b": structpb.NewNumberValue(1), "struct_value": structpb.NewStringValue("data"),
	}}
	roots := []proto.Message{
		s, structpb.NewStructValue(s), &structpb.ListValue{Values: []*structpb.Value{structpb.NewStructValue(s)}},
		&emptypb.Empty{}, nil, (*structpb.Struct)(nil), (*dynamicpb.Message)(nil), structpb.NewNumberValue(1),
		dynamicpb.NewMessage(s.ProtoReflect().Descriptor()),
		&pb.Account{Owner: &pb.Owner{Email: "e", Org: "o"}, Delegates: map[string]*pb.Owner{"k": {Email: "e", Org: "o"}}},
		parseMessageChoice(t, messageChoiceDescriptor(t), `config { a {} }`),
	}
	segments := []string{"color", "fields", "values", "struct_value", "items", "owner", "note", "org", "delegates", "k", "0", "*", "`a.b`", "", "`", "config", "a"}

	for i := range 4096 {
		req := round8Clone(roots[rng.IntN(len(roots))])
		stored := round8Clone(roots[rng.IntN(len(roots))])
		raw := &fieldmaskpb.FieldMask{}

		for range rng.IntN(5) {
			parts := make([]string, 1+rng.IntN(4))
			for j := range parts {
				parts[j] = segments[rng.IntN(len(segments))]
			}

			raw.Paths = append(raw.Paths, strings.Join(parts, "."))
		}

		if rng.IntN(8) == 0 {
			raw = nil
		}

		entry, known := round8Entries[i%len(round8Entries)], rng.IntN(2) == 0
		t.Run(fmt.Sprintf("%04d/%s", i, entry), func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("panic: %v; req=%T stored=%T mask=%v current=%t", p, req, stored, raw, known)
				}
			}()

			got, err := round8Entry(t, entry, req, stored, raw, known)
			if err == nil && got == nil && !(entry == "Apply" && raw == nil) {
				t.Fatal("entry returned neither an error nor a mask")
			}
		})
	}
}
