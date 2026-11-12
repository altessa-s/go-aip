// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package aipmap_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/altessa-s/go-aip/fieldmask/update"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Literal expectations pin the reviewer's case and selection/covered-mask
// variants independently of both the production join and the golden generator.
func TestWildcardExplicitKeyRefusalOrder(t *testing.T) {
	for _, tc := range []struct {
		name      string
		keys, raw []string
		order     []string
		index     int
	}{
		{"reviewer", []string{"a", "z"}, []string{"delegates.a.org", "delegates.*.org"},
			[]string{"delegates.z.org", "delegates.a.org"}, 1},
		{"reversed", []string{"a", "z"}, []string{"delegates.*.org", "delegates.a.org"},
			[]string{"delegates.z.org", "delegates.a.org"}, 0},
		{"multiple keys", []string{"a", "b", "y", "z"}, []string{"delegates.b.org", "delegates.a.org", "delegates.*.org"},
			[]string{"delegates.y.org", "delegates.z.org", "delegates.a.org", "delegates.b.org"}, 2},
		{"quoted key", []string{"a.b", "z"}, []string{"delegates.`a.b`.org", "delegates.*.org"},
			[]string{"delegates.z.org", "delegates.`a.b`.org"}, 1},
		{"key before wildcard", []string{"!", "a", "z"}, []string{"delegates.a.org", "delegates.*.org", "delegates.!.org"},
			[]string{"delegates.!.org", "delegates.z.org", "delegates.a.org"}, 1}, // Equal-length matches use the first raw path, including wildcards.
		{"covered wildcard first", []string{"a", "z"}, []string{"delegates.*.org", "delegates", "delegates.a.org"},
			[]string{"delegates.a.org", "delegates.z.org"}, 0},
		{"covered key first", []string{"a", "z"}, []string{"delegates.a.org", "delegates", "delegates.*.org"},
			[]string{"delegates.a.org", "delegates.z.org"}, 0},
	} {
		for _, deferred := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/deferred=%t", tc.name, deferred), func(t *testing.T) {
				c := generatedRefusalCase{Layout: "keyed-map", Keys: tc.keys, Raw: tc.raw}
				for i := range tc.keys {
					c.Changed = append(c.Changed, i)
				}

				req, stored, raw, opts := generatedRefusalFixture(c)
				before := proto.Clone(stored)

				spec, err := update.NewSpec(req, opts...)
				if err != nil {
					t.Fatal(err)
				}

				ctx := t.Context()
				planOptions := []update.Option{update.WithCurrent(stored)}

				var aggregate error

				if deferred {
					pending, err := update.Prepare(raw, req, opts...)
					if err != nil {
						t.Fatal(err)
					}

					_, aggregate = pending.Complete(stored)
					ctx = update.ContextWithPlan(ctx, pending)
					planOptions = append(planOptions, update.WithPlanContext(ctx))
					raw = pending.Mask().ToProtoFieldMask()
				} else {
					_, aggregate = update.ApplyMask(raw, proto.Clone(req), append(slices.Clone(opts), update.WithCurrent(stored))...)
				}

				var behaviorError *update.BehaviorError
				if !errors.As(aggregate, &behaviorError) || len(behaviorError.Violations) != len(tc.order) {
					t.Fatalf("aggregate = %v, want %v", aggregate, tc.order)
				}

				for i, v := range behaviorError.Violations {
					if v.Path != tc.order[i] {
						t.Fatalf("aggregate = %v, want %v", behaviorError.Violations, tc.order)
					}
				}

				_, err = spec.Plan(req, raw, planOptions...)
				assertWildcardLocation(t, err, fmt.Sprintf("updateMask.paths[%d]", tc.index), tc.order[0], tc.index, tc.raw[tc.index])

				patch := refusalPatch(spec, stored)
				_, err = patch.Run(ctx, req, "", raw)
				assertWildcardLocation(t, err, "resource."+tc.order[0], tc.order[0], tc.index, tc.raw[tc.index])

				if !proto.Equal(stored, before) {
					t.Fatal("comparison mutated stored state")
				}
			})
		}
	}
}

func assertWildcardLocation(t *testing.T, err error, outer, field string, index int, raw string) {
	t.Helper()

	var (
		fieldError *coreerrs.FieldError
		pathError  *update.MaskPathError
	)
	if !errors.Is(err, update.ErrProtectedField) || !errors.Is(err, coreerrs.ErrInvalidArgument) ||
		!errors.As(err, &fieldError) || fieldError.Field != outer || !errors.As(err, &pathError) ||
		pathError.Field != field || pathError.Index != index || pathError.Path != raw {
		t.Fatalf("refusal = %v, want %s, field %s at raw mask[%d]=%q", err, outer, field, index, raw)
	}
}
