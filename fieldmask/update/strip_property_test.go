// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// These pipelines have no mask, as StripUpdate has none. The generated oracle
// paths include the protected input, before normalization removes equal values.
func checkGeneratedStripPipelines(t *testing.T, rng *rand.Rand, count int) {
	t.Helper()
	md := messageChoiceDescriptor(t)
	choices := []string{``, `config { a {} }`, `config { a { note: "same" } }`, `config { a { note: "other" } }`,
		`config { b: "same" }`, `config { b: "other" }`, `config { b: "" }`}

	for i := range count {
		b := annotations.FieldBehavior_IMMUTABLE
		if rng.IntN(2) == 0 {
			b = annotations.FieldBehavior_IDENTIFIER
		}

		protected := []string{"org", "a", "b", "note"}[i%4]
		resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if string(fd.Name()) == protected {
				return []annotations.FieldBehavior{b}
			}

			return fieldbehavior.AnnotationResolver{}.Resolve(fd)
		})
		opts := []update.Option{update.WithResolver(resolver)}

		var (
			req, stored proto.Message
			oracle      []string
		)

		if i%4 == 0 {
			atomic := rng.IntN(2) == 0
			if atomic {
				opts = append(opts, atomicNames("fieldbehaviortest.v1.Owner"))
			}

			account := func() *pb.Account {
				a := &pb.Account{}
				if rng.IntN(4) != 0 {
					a.Owner = &pb.Owner{Email: "e", Org: []string{"", "same", "other"}[rng.IntN(3)], Note: []string{"", "old", "new"}[rng.IntN(3)]}
				}

				for range rng.IntN(3) {
					a.Members = append(a.Members, &pb.Owner{Email: "e", Org: []string{"", "same"}[rng.IntN(2)], Note: "new"})
				}

				if rng.IntN(2) == 0 {
					a.Delegates = map[string]*pb.Owner{"k": {Email: "e", Org: []string{"", "same"}[rng.IntN(2)], Note: "new"}}
				}

				return a
			}
			r, c := account(), account()

			req, stored = r, c
			if r.Owner != nil {
				if atomic {
					oracle = append(oracle, "owner")
				} else {
					oracle = append(oracle, "owner.email")
					if r.Owner.Org != "" {
						oracle = append(oracle, "owner.org")
					}

					if r.Owner.Note != "" {
						oracle = append(oracle, "owner.note")
					}
				}
			}

			if len(r.Members) > 0 {
				oracle = append(oracle, "members")
			}

			if len(r.Delegates) > 0 {
				oracle = append(oracle, "delegates")
			}
		} else {
			req = parseMessageChoice(t, md, choices[rng.IntN(len(choices))])
			stored = parseMessageChoice(t, md, choices[rng.IntN(len(choices))])
			root := req.ProtoReflect()

			config := root.Descriptor().Fields().ByName("config")
			if root.Has(config) {
				m := root.Get(config).Message()

				a, bf := m.Descriptor().Fields().ByName("a"), m.Descriptor().Fields().ByName("b")
				if m.Has(bf) {
					oracle = append(oracle, "config.b")
				}

				if m.Has(a) {
					if protected == "a" {
						oracle = append(oracle, "config.a")
					} else if m.Get(a).Message().Has(a.Message().Fields().ByName("note")) {
						oracle = append(oracle, "config.a.note")
					}
				}
			}
		}

		if rng.IntN(4) == 0 {
			stored = proto.Clone(req)
		}

		t.Run(fmt.Sprintf("strip=%04d/%s/%s", i, b, protected), func(t *testing.T) {
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("seed=(203,3) strip=%d paths=%v request=%v stored=%v", i, oracle, req, stored)
				}
			})

			effect := proto.Clone(stored)
			if err := update.MergePaths(effect, req, oracle, opts...); err != nil {
				t.Fatal(err)
			}

			unchanged := oracleProtectedEqual(stored.ProtoReflect(), effect.ProtoReflect(), resolver)

			spec, err := update.NewSpec(req, opts...)
			if err != nil {
				t.Fatal(err)
			}

			for _, patch := range []bool{false, true} {
				direct, directErr := runStripPipeline(t, spec, proto.Clone(req), stored, patch, opts...)
				if (directErr == nil) != unchanged {
					t.Fatalf("one-stage invariant: unchanged=%t patch=%t error=%v", unchanged, patch, directErr)
				}

				for _, known := range []bool{false, true} {
					input := proto.Clone(req)

					stripOpts := []fieldbehavior.Option{fieldbehavior.WithResolver(resolver)}
					if known {
						stripOpts = append(stripOpts, fieldbehavior.WithCurrent(stored))
					}

					err := fieldbehavior.StripUpdate(input, stripOpts...)

					var got proto.Message
					if err == nil {
						got, err = runStripPipeline(t, spec, input, stored, patch, opts...)
					}

					if (err == nil) != (directErr == nil) {
						t.Fatalf("pipeline mismatch current=%t patch=%t: direct=%v pipeline=%v", known, patch, directErr, err)
					}

					if err == nil {
						requireProtoEqual(t, direct, got)

						if !oracleProtectedEqual(stored.ProtoReflect(), got.ProtoReflect(), resolver) {
							t.Fatalf("accepted protected change current=%t patch=%t", known, patch)
						}
					}
				}
			}
		})
	}
}

func runStripPipeline(t *testing.T, spec *update.Spec, req, stored proto.Message, patch bool, opts ...update.Option) (proto.Message, error) {
	t.Helper()

	original := proto.Clone(req)

	if !patch {
		plan, err := spec.Plan(req, nil, update.WithCurrent(stored))
		if err != nil {
			return nil, err
		}

		update.AssertWritableSelection(t, nil, original, fieldmask.FromPaths(plan.Paths...), opts...)

		got := proto.Clone(stored)
		err = update.MergePaths(got, req, plan.Paths)

		return got, err
	}

	p := update.Patch[proto.Message, proto.Message, *int64]{
		Field: "resource", Spec: spec,
		Precondition: update.Int64Precondition[proto.Message]{Version: func(proto.Message) int64 { return 1 }},
		Read:         func(context.Context) (proto.Message, error) { return proto.Clone(stored), nil },
		ToProto:      func(m proto.Message) proto.Message { return m },
		Save: func(_ context.Context, m proto.Message, plan update.Plan, _ *int64) (proto.Message, error) {
			update.AssertWritableSelection(t, nil, original, fieldmask.FromPaths(plan.Paths...), opts...)
			return m, nil
		},
	}

	return p.Run(t.Context(), req, "", nil)
}

// Independent protobuf projection: it does not use the implementation's equality
// or selection helpers. A holder preserves presence only of a protected field.
func oracleProtectedEqual(a, b protoreflect.Message, resolver fieldbehavior.Resolver, top ...fieldbehavior.Resolver) bool {
	template := a
	if template == nil {
		template = b
	}

	if template == nil {
		return true
	}

	if a == nil {
		a = template.Type().Zero()
	}

	if b == nil {
		b = template.Type().Zero()
	}

	fields := template.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)

		classify := resolver
		if len(top) > 0 {
			classify = top[0]
		}

		var ignored, protected bool
		for _, v := range classify.Resolve(fd) {
			ignored = ignored || v == annotations.FieldBehavior_OUTPUT_ONLY
			protected = protected || v == annotations.FieldBehavior_IMMUTABLE || v == annotations.FieldBehavior_IDENTIFIER
		}

		if ignored {
			continue
		}

		if protected {
			value := func(m protoreflect.Message) proto.Message {
				holder := m.Type().New()
				if m.Has(fd) {
					holder.Set(fd, m.Get(fd))
				}

				out := proto.Clone(holder.Interface())
				clearOracleOutput(out.ProtoReflect(), resolver)

				return out
			}
			if !proto.Equal(value(a), value(b)) {
				return false
			}

			continue
		}

		switch {
		case fd.IsList() && fd.Message() != nil:
			av, bv := a.Get(fd).List(), b.Get(fd).List()
			for j := range max(av.Len(), bv.Len()) {
				var am, bm protoreflect.Message
				if j < av.Len() {
					am = av.Get(j).Message()
				}

				if j < bv.Len() {
					bm = bv.Get(j).Message()
				}

				if !oracleProtectedEqual(am, bm, resolver) {
					return false
				}
			}
		case fd.IsMap() && fd.MapValue().Message() != nil:
			av, bv := a.Get(fd).Map(), b.Get(fd).Map()
			equal := true

			for _, values := range []protoreflect.Map{av, bv} {
				values.Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool {
					var am, bm protoreflect.Message
					if av.Has(k) {
						am = av.Get(k).Message()
					}

					if bv.Has(k) {
						bm = bv.Get(k).Message()
					}

					equal = equal && oracleProtectedEqual(am, bm, resolver)

					return equal
				})
			}

			if !equal {
				return false
			}
		case !fd.IsList() && !fd.IsMap() && fd.Message() != nil:
			var am, bm protoreflect.Message
			if a.Has(fd) {
				am = a.Get(fd).Message()
			}

			if b.Has(fd) {
				bm = b.Get(fd).Message()
			}

			if !oracleProtectedEqual(am, bm, resolver) {
				return false
			}
		}
	}

	return true
}
