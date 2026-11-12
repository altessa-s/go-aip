// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"
	"github.com/altessa-s/go-aip/internal/maskmerge"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// The fixed seed and numbered subtests make every failure independently
// reproducible. Keep REQUIRED enabled: absent request lists must still validate
// the stored elements reached by a selected descendant.
func TestDeferredGeneratedEquivalence(t *testing.T) {
	const count = 240

	rng := rand.New(rand.NewPCG(203, 3))
	paths := [][]string{
		nil, {}, {"*"}, {"display_name"}, {"owner"}, {"owner.note"}, {"owner.email"},
		{"owner.create_time"}, {"parent.owner.note"}, {"members"}, {"members.*.note"},
		{"members.*.email"}, {"members.*.org"}, {"delegates"}, {"delegates.k"},
		{"delegates.k.note"}, {"delegates.*.note", "delegates.k"},
		{"delegates.*.note", "delegates.k.org"}, {"delegates.*.note", "delegates.k.create_time"}, {"delegates.*.email", "delegates.k.create_time"},
		{"settings"}, {"settings.defaults.theme"}, {"region"}, {"owner", "owner.note"},
	}
	owner := func() *pb.Owner {
		switch rng.IntN(5) {
		case 0:
			return nil
		case 1:
			return &pb.Owner{}
		default:
			return &pb.Owner{Email: "e", Org: []string{"", "same", "other"}[rng.IntN(3)], Note: []string{"", "n"}[rng.IntN(2)], CreateTime: "ignored"}
		}
	}

	account := func() *pb.Account {
		a := &pb.Account{DisplayName: "d", Key: []byte("k"), Enabled: true, State: pb.State(1), Quota: new(int32(1)),
			Emails: []string{"e"}, Attributes: map[string]string{"k": "v"}, Billing: &pb.Account_Card{Card: "c"}, Owner: owner()}
		if rng.IntN(2) == 0 {
			a.Parent = &pb.Account{Owner: owner()}
		}

		for range rng.IntN(3) {
			o := owner()
			if o == nil {
				o = &pb.Owner{}
			}

			a.Members = append(a.Members, o)
		}

		if rng.IntN(2) == 0 {
			a.Delegates = map[string]*pb.Owner{}
		}

		for _, key := range []string{"k", "a", "b", "*"} {
			if o := owner(); o != nil && rng.IntN(2) == 0 {
				if a.Delegates == nil {
					a.Delegates = map[string]*pb.Owner{}
				}

				a.Delegates[key] = o
			}
		}

		if rng.IntN(2) == 0 {
			a.Settings = &pb.Settings{Theme: "t", UpdatedBy: "ignored"}
			if rng.IntN(2) == 0 {
				a.Settings.Defaults = &pb.Settings{Theme: "nested"}
			}
		}

		return a
	}
	for i := range count {
		req, stored := account(), account()
		if rng.IntN(4) == 0 {
			stored = proto.CloneOf(req)
		}

		// Cycle selection/policy/limit pairs; randomize only the payloads.
		raw := maskOf(paths[i%len(paths)])
		policy := (i / len(paths)) % 5
		limit := (i/len(paths) + i) % 5

		opts := []update.Option{}
		if i%4 == 0 {
			opts = append(opts, atomicNames("fieldbehaviortest.v1.Owner"))
		}

		switch policy {
		case 1, 2:
			b := annotations.FieldBehavior_IMMUTABLE
			if policy == 2 {
				b = annotations.FieldBehavior_IDENTIFIER
			}

			opts = append(opts, update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
				if fd.Name() == "owner" || fd.Name() == "org" {
					return []annotations.FieldBehavior{b}
				}

				return fieldbehavior.AnnotationResolver{}.Resolve(fd)
			})))
		case 3:
			opts = append(opts, update.WithImmutableFields("owner", "region"))
		case 4:
			opts = append(opts, update.WithIdentityFields("owner", "region"))
		}

		switch limit {
		case 1:
			opts = append(opts, update.WithMaxPaths(2))
		case 2:
			opts = append(opts, update.WithMaxPathDepth(2))
		case 3:
			opts = append(opts, update.WithMaxPathLength(20))
		case 4:
			opts = append(opts, update.WithMaxPaths(1))
		}

		t.Run(fmt.Sprintf("case=%04d/policy=%d/limit=%d", i, policy, limit), func(t *testing.T) {
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("seed=(203,3) case=%d policy=%d limit=%d mask=%v request=%v stored=%v", i, policy, limit, raw, req, stored)
				}
			})
			// Early REQUIRED rejection is the documented diagnostic exception:
			// stored-dependent violations or final expansion refusals are not
			// available yet. Neither path may accept this input.
			_, early := update.Prepare(raw, proto.Clone(req), opts...)
			if be, ok := errors.AsType[*update.BehaviorError](early); ok {
				_, direct := update.ApplyMask(raw, proto.Clone(req), append(slices.Clone(opts), update.WithCurrent(stored))...)
				if direct == nil {
					t.Fatalf("early REQUIRED refusal but stateful success: %v", early)
				}

				for _, v := range be.Violations {
					if v.Behavior != annotations.FieldBehavior_REQUIRED {
						t.Fatalf("undocumented early refusal: %v", early)
					}
				}

				if errors.Is(direct, fieldmask.ErrInvalidPath) {
					return
				}
			}

			checkDeferredEquivalence(t, raw, req, stored, opts...)
		})
	}

	const (
		effectCount   = 112
		pipelineCount = 64
	)

	checkGeneratedProtectedEffects(t, rng, effectCount)
	checkGeneratedStripPipelines(t, rng, pipelineCount)
	checkGeneratedOriginalSelections(t, rng, pipelineCount)
	t.Logf("checked %d deterministic cases (seed 203,3): %d broad equivalence + %d independent merge-effect invariants + %d stripping + %d original-selection cases", count+effectCount+2*pipelineCount, count, effectCount, pipelineCount, pipelineCount)
}

// The oracle uses public MergePaths on original input and compares protobuf
// values directly, without the protected comparator or normalization under test.
// Wildcards are expanded to the request's original targets before ignoring the
// keyed OUTPUT_ONLY selection. These cases isolate one protected ancestor, so a
// changed effect must be rejected and an unchanged effect must succeed.
func checkGeneratedProtectedEffects(t *testing.T, rng *rand.Rand, count int) {
	t.Helper()

	md := messageChoiceDescriptor(t)
	choices := []string{
		``, `config {}`, `config { a {} }`, `config { b: "" }`, `config { b: "stored" }`,
		`config { a { note: "n" stamp: "client" } }`, `config { a { stamp: "server" } }`,
		`config { child {} }`, `config { child { note: "n" } b: "stored" }`,
	}
	oneofPaths := [][]string{
		{"config"}, {"config.a.note"}, {"config.b"}, {"config.child.note"},
		{"config.a.stamp"}, {"config.a.note", "config.child.note"}, {"config.a.note", "config.b"},
	}

	for i := range count {
		b := annotations.FieldBehavior_IMMUTABLE
		if rng.IntN(2) == 0 {
			b = annotations.FieldBehavior_IDENTIFIER
		}

		policy := rng.IntN(2) == 0

		var req, stored proto.Message

		var paths, oraclePaths []string

		protected := "config"

		if i%4 == 0 {
			req = parseMessageChoice(t, md, choices[rng.IntN(len(choices))])
			stored = parseMessageChoice(t, md, choices[rng.IntN(len(choices))])
			paths = oneofPaths[rng.IntN(len(oneofPaths))]

			oraclePaths = paths
			if slices.Equal(paths, []string{"config.a.stamp"}) {
				oraclePaths = nil // Ignored paths do not traverse or switch a oneof.
			}
		} else if i%4 < 3 {
			account := func() *pb.Account {
				return &pb.Account{Parent: &pb.Account{Emails: []string{[]string{"old", "new"}[rng.IntN(2)]}, Attributes: map[string]string{"k": []string{"old", "new"}[rng.IntN(2)]}}}
			}
			req, stored, protected = account(), account(), "parent"

			paths = []string{"parent.emails.*"}
			if i%4 == 2 {
				paths = []string{"parent.attributes.*"}
			}

			oraclePaths = paths
		} else {
			account := func() *pb.Account {
				m := &pb.Account{Delegates: map[string]*pb.Owner{}}

				for _, key := range []string{"k", "a", "b"} {
					if rng.IntN(3) != 0 {
						m.Delegates[key] = &pb.Owner{Email: "e", Note: []string{"", "n", "other"}[rng.IntN(3)], CreateTime: "ignored"}
					}
				}

				return m
			}
			r, c := account(), account()
			paths = []string{"delegates.*.note", "delegates.k.create_time"}

			for _, key := range []string{"a", "b"} {
				if r.Delegates[key] != nil {
					oraclePaths = append(oraclePaths, "delegates."+key+".note")
				}
			}

			req, stored, protected = r, c, "delegates"
			if rng.IntN(2) == 0 {
				req, stored, protected = &pb.Account{Parent: r}, &pb.Account{Parent: c}, "parent"
				paths = []string{"parent.delegates.*.note", "parent.delegates.k.create_time"}

				for j := range oraclePaths {
					oraclePaths[j] = "parent." + oraclePaths[j]
				}
			}
		}

		t.Run(fmt.Sprintf("effect=%04d/%s/policy=%t", i, b, policy), func(t *testing.T) {
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("seed=(203,3) effect=%d mask=%v oracle=%v protected=%s request=%v stored=%v", i, paths, oraclePaths, protected, req, stored)
				}
			})

			opts := []update.Option{protectedOption(protected, b, policy), update.WithMaxPaths(len(paths))}
			checkDeferredEquivalence(t, maskOf(paths), req, stored, opts...)
			assertProtectedMergeEffect(t, req, stored, paths, oraclePaths, protected, b, opts)
		})
	}
}

func assertProtectedMergeEffect(t *testing.T, req, stored proto.Message, paths, oraclePaths []string,
	protected string, b annotations.FieldBehavior, opts []update.Option,
) {
	t.Helper()

	expected := proto.Clone(stored)
	if err := update.MergePaths(expected, req, oraclePaths); err != nil {
		t.Fatal(err)
	}

	want := protectedInputValue(stored, protected)
	unchanged := proto.Equal(want, protectedInputValue(expected, protected))

	resolved := make([]string, 0, len(paths))
	for _, path := range paths {
		resolved = append(resolved, update.ResolvePath(req.ProtoReflect().Descriptor(), path, opts...))
	}

	resolve := func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if string(fd.Name()) == protected {
			return []annotations.FieldBehavior{b}
		}

		return fieldbehavior.AnnotationResolver{}.Resolve(fd)
	}

	changes, err := maskmerge.ProtectedChanges(req.ProtoReflect(), stored.ProtoReflect(), fieldmask.FromPaths(resolved...), resolve, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	if (len(changes) == 0) != unchanged {
		t.Fatalf("comparator differs from MergePaths: unchanged=%t changes=%v", unchanged, changes)
	}

	for _, deferred := range []bool{false, true} {
		normalized := proto.Clone(req)

		var mask fieldmask.FieldMask

		var err error

		if deferred {
			var pending *update.DeferredPlan

			pending, err = update.Prepare(maskOf(paths), normalized, opts...)
			if err == nil {
				mask, err = pending.Complete(stored)
			}
		} else {
			mask, err = update.ApplyMask(maskOf(paths), normalized, append(slices.Clone(opts), update.WithCurrent(stored))...)
		}

		if unchanged && err != nil {
			t.Fatalf("unchanged protected merge rejected (deferred=%t): %v", deferred, err)
		}

		if !unchanged {
			be, ok := errors.AsType[*update.BehaviorError](err)
			if !ok || len(be.Violations) != 1 || be.Violations[0].Path != protected || be.Violations[0].Behavior != b {
				t.Fatalf("changed protected merge must reject %s (deferred=%t): %v", protected, deferred, err)
			}
		}

		if err == nil {
			update.AssertWritableSelection(t, maskOf(paths), req, mask, opts...)

			merged := proto.Clone(stored)
			if err := update.MergePaths(merged, normalized, mask.ToPaths(), opts...); err != nil {
				t.Fatal(err)
			}

			if !proto.Equal(want, protectedInputValue(merged, protected)) {
				t.Fatalf("accepted merge changed protected %s (deferred=%t): stored=%v merged=%v", protected, deferred, stored, merged)
			}
		}
	}
}

// Preserve field presence while excluding only OUTPUT_ONLY descendants. This
// deliberately does not call the implementation's SameInputValue comparator.
func protectedInputValue(resource proto.Message, name string) proto.Message {
	m := resource.ProtoReflect()
	fd := m.Descriptor().Fields().ByName(protoreflect.Name(name))

	holder := m.Type().New()
	if m.Has(fd) {
		holder.Set(fd, m.Get(fd))
	}

	value := proto.Clone(holder.Interface())
	clearOracleOutput(value.ProtoReflect())

	return value
}

func clearOracleOutput(m protoreflect.Message, resolvers ...fieldbehavior.Resolver) {
	var resolver fieldbehavior.Resolver = fieldbehavior.AnnotationResolver{}
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}

	m.Range(func(fd protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if slices.Contains(resolver.Resolve(fd), annotations.FieldBehavior_OUTPUT_ONLY) {
			m.Clear(fd)
			return true
		}

		switch {
		case fd.IsMap() && fd.MapValue().Message() != nil:
			value.Map().Range(func(_ protoreflect.MapKey, v protoreflect.Value) bool {
				clearOracleOutput(v.Message(), resolver)
				return true
			})
		case fd.IsList() && fd.Message() != nil:
			for i := range value.List().Len() {
				clearOracleOutput(value.List().Get(i).Message(), resolver)
			}
		case !fd.IsMap() && !fd.IsList() && fd.Message() != nil:
			clearOracleOutput(value.Message(), resolver)
		}

		return true
	})
}
