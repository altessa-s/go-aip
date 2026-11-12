// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package aipmap_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"

	"github.com/altessa-s/go-aip/fieldmask/update"
)

// These expectations are literal resource paths, independent of the update
// evaluator and sorter. TestPublicRefusalBaseParity checks their public errors
// against the gzip recorded at a8b17ba, under every wrapper. The two-stage
// corpus additionally checks Complete and direct/deferred Spec and Patch calls.
func orderedRefusalViolations(c publicRefusalCase) []update.FieldViolation {
	var paths, identifiers []string

	parts := strings.Split(c.Kind, "/")
	layout, mode := parts[1], parts[2]
	planning := c.Entry == "Spec" || c.Entry == "Patch"
	treeOrder := mode != "all" && !(mode == "implied" && planning)

	switch layout {
	case "atomic-after":
		paths = []string{"owner.org", "region"}
	case "atomic-between", "nested-between":
		paths, identifiers = []string{"name", "owner.org", "region"}, []string{"name"}
	case "atomic-priority":
		paths = []string{"name", "owner.org", "region"}
		if mode == "implied" && planning {
			paths = []string{"owner.org", "name", "region"}
		}
	case "nested-deep":
		paths, identifiers = []string{"name", "region", "parent.owner.org"}, []string{"name"}
		if treeOrder {
			paths = []string{"name", "parent.owner.org", "region"}
		}
	case "nested-late":
		paths = []string{"region", "parent.owner.org"}
		if treeOrder {
			paths = []string{"parent.owner.org", "region"}
		}
	case "atomic-collection":
		paths = []string{"region", "parent.members.0.org"}
	case "covered-clear":
		paths = []string{"region", "parent.owner.org"}
	case "list-nested":
		paths = []string{"message_type.1.options.message_set_wire_format", "message_type.0.options.deprecated"}
	case "list-atomic":
		paths = []string{"message_type.0.options.deprecated", "message_type.1.options.message_set_wire_format"}
	case "lexical-mixed":
		paths = []string{"owner.note", "region", "parent.owner.org"}
		if treeOrder {
			paths = []string{"owner.note", "parent.owner.org", "region"}
		}
	case "atomic-fields", "nested-fields":
		paths, identifiers = []string{"owner.email", "owner.org", "owner.note"}, []string{"owner.email"}
		if layout == "nested-fields" && treeOrder && mode != "covering" {
			paths = []string{"owner.email", "owner.note", "owner.org"}
		}
	case "list-sibling":
		paths = []string{"region", "members.0.org"}
	case "list":
		paths, identifiers = []string{"members.2.org", "members.10.email", "members.10.org"}, []string{"members.10.email"}
		if mode != "all" && mode != "implied" && !(mode == "covering" && (c.Entry == "Apply" || c.Entry == "ValidateStrict")) {
			paths = []string{"members.10.email", "members.2.org", "members.10.org"}
		}
	case "map":
		paths = []string{"delegates.a-b.email", "delegates.`a.b`.org", "delegates.z.org"}
		identifiers = []string{"delegates.a-b.email"}
	default:
		panic(c.Kind)
	}

	violations := make([]update.FieldViolation, 0, len(paths))
	for _, path := range paths {
		v := update.FieldViolation{Path: path, Behavior: annotations.FieldBehavior_IMMUTABLE,
			Reason: "immutable field cannot be changed from its stored value"}
		if slices.Contains(identifiers, path) {
			v.Behavior, v.Reason = annotations.FieldBehavior_IDENTIFIER, "identifier field cannot be changed from its stored value"
		}

		violations = append(violations, v)
	}

	return violations
}

// baseRepresentedViolations accounts only for diagnostics added since the
// base: collection replacements were accepted, element paths were collapsed
// to wildcards, and atomic messages reported only their first change. It never
// reorders fields. The complete concrete order is asserted independently above.
func baseRepresentedViolations(c publicRefusalCase, violations []update.FieldViolation) []update.FieldViolation {
	parts := strings.Split(c.Kind, "/")

	layout, mode := parts[1], parts[2]
	if layout == "atomic-fields" || layout == "list-sibling" || layout == "atomic-collection" {
		return violations[:1]
	}

	if layout != "list" && layout != "map" && layout != "list-nested" {
		return violations
	}

	if mode == "all" || mode == "implied" || mode == "covering" && (c.Entry == "Apply" || c.Entry == "ValidateStrict") {
		return nil // the base accepted whole collection replacements
	}

	var out []update.FieldViolation

	for _, v := range violations {
		field := v.Path[strings.LastIndex(v.Path, ".")+1:]

		prefix := "members.*."
		if layout == "list-nested" {
			prefix = "message_type.*.options."
		}

		if layout == "map" {
			prefix = "delegates.*."
		}

		v.Path = prefix + field
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}

	return out
}

func assertOrderedRefusal(t *testing.T, c publicRefusalCase, base refusalRecord, err error, code int32, fields []string) {
	t.Helper()

	violations := orderedRefusalViolations(c)

	want := orderedRefusalShape(c, violations)
	if got := refusalTree(err); !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered refusal:\ngot  %#v\nwant %#v", got, want)
	}

	wantFields := orderedRefusalMappedFields(c, base.Sequence, violations)
	if code != 3 || !slices.Equal(fields, wantFields) {
		t.Fatalf("ordered mapping: code=%d fields=%v, want INVALID_ARGUMENT %v", code, fields, wantFields)
	}

	// Compare every base-representable field in its recorded order, including
	// nested-late and all explicit-mask permutations. No ordering exemptions.
	represented := baseRepresentedViolations(c, violations)

	baseWant, baseFields, baseCode := refusalShape{}, []string(nil), int32(0)
	if len(represented) > 0 {
		baseWant = orderedRefusalShape(c, represented)
		baseFields = orderedRefusalMappedFields(c, base.Sequence, represented)
		baseCode = 3
	}

	if !reflect.DeepEqual(base.Shape, baseWant) || base.Code != baseCode || !slices.Equal(base.Fields, baseFields) {
		t.Fatalf("ordered refusal must equal base:\nbase %#v / %v\nwant %#v / %v", base.Shape, base.Fields, baseWant, baseFields)
	}
}

func orderedRefusalShape(c publicRefusalCase, violations []update.FieldViolation) refusalShape {
	if c.Entry != "Spec" && c.Entry != "Patch" {
		if c.Entry == "ValidateStrict" || c.Entry == "ApplyMask" && c.Strict {
			return refusalShape{Type: "*update.StrictError", Violations: violations, Identities: []string{"behavior", "strict"},
				Children: []refusalShape{refusalSentinel("strict"), refusalSentinel("behavior")}}
		}

		return refusalShape{Type: "*update.BehaviorError", Violations: violations, Identities: []string{"behavior"},
			Children: []refusalShape{refusalSentinel("behavior")}}
	}

	index, raw := orderedRefusalLocation(c)

	field := "updateMask"
	if index >= 0 {
		field = fmt.Sprintf("updateMask.paths[%d]", index)
	}

	want := refusalShape{Type: "*errors.FieldError", Field: field, Identities: []string{"invalid-argument", "protected"},
		Children: []refusalShape{refusalSentinel("invalid-argument"), {
			Type: "*update.MaskPathError", Field: violations[0].Path, Index: index, Path: raw, Identities: []string{"protected"},
			Children: []refusalShape{{Type: "*update.changedError", Identities: []string{"protected"},
				Children: []refusalShape{refusalSentinel("protected")}}},
		}}}
	if c.Entry == "Patch" {
		want = refusalShape{Type: "*errors.FieldError", Field: "resource." + violations[0].Path,
			Identities: []string{"invalid-argument", "protected"}, Children: []refusalShape{refusalSentinel("invalid-argument"), want}}
	}

	return want
}

func refusalSentinel(identity string) refusalShape {
	return refusalShape{Type: "*errors.errorString", Identities: []string{identity}}
}

// Literal provenance for the first reported field, including the longest
// covering path and the original index when the client reverses its mask.
func orderedRefusalLocation(c publicRefusalCase) (int, string) {
	parts := strings.Split(c.Kind, "/")

	layout, mode := parts[1], parts[2]
	if mode == "all" || mode == "implied" {
		return -1, ""
	}

	var (
		path                         string
		declared, reversed, covering int
	)

	switch layout {
	case "atomic-after":
		path, reversed = "owner", 1
	case "atomic-between", "atomic-priority", "nested-between", "nested-deep":
		path, reversed = "name", 2
	case "nested-late":
		path, declared, covering = "parent.owner.org", 1, 3
	case "atomic-collection":
		path, reversed = "region", 1
	case "covered-clear":
		path, declared, reversed = "region", 1, 1
	case "list-nested", "list-atomic":
		path = "message_type.*.options"
	case "lexical-mixed":
		path, reversed, covering = "owner.note", 2, 3
	case "atomic-fields":
		path = "owner"
	case "nested-fields":
		path, reversed, covering = "owner.email", 2, 1
	case "list-sibling":
		path, declared, covering = "region", 1, 1
	case "list":
		path, reversed, covering = "members.*.email", 1, 1
	case "map":
		path, reversed, covering = "delegates.*.email", 1, 1
	default:
		panic(layout)
	}

	switch mode {
	case "declared":
		return declared, path
	case "reversed":
		return reversed, path
	case "shuffled":
		_, _, raw, _, _ := orderedRefusalFixture(c)
		return slices.Index(raw.Paths, path), path
	case "covering":
		return covering, path
	default:
		panic(mode)
	}
}

func orderedRefusalMappedFields(c publicRefusalCase, sequence []int, violations []update.FieldViolation) []string {
	if c.Entry == "Spec" || c.Entry == "Patch" {
		field := "updateMask"
		if index, _ := orderedRefusalLocation(c); index >= 0 {
			field = fmt.Sprintf("updateMask.paths[%d]", index)
		}

		if c.Entry == "Patch" {
			field = "resource." + violations[0].Path
		}

		return wrappedPlanRefusalPaths(sequence, field)
	}

	if slices.Contains(sequence, 16) || slices.Contains(sequence, 18) {
		return []string{"book.uid"}
	}

	var fields []string
	for _, violation := range violations {
		fields = append(fields, aggregateRefusalPaths(sequence, violation.Path)...)
	}

	return fields
}

// Every deferred first field and complete violation list is tied directly to
// the same base recording as its one-stage entry point, without order skips.
func TestOrderedRefusalDeferredBaseParity(t *testing.T) {
	mapper := newMapper(t)
	for _, record := range readRefusalParity(t) {
		c := record.Case
		if len(record.Sequence) != 0 || !strings.HasPrefix(c.Kind, "order/") ||
			(c.Entry != "ApplyMask" && c.Entry != "Spec" && c.Entry != "Patch") {
			continue
		}

		t.Run(fmt.Sprintf("%s/%s/strict=%t", c.Entry, c.Kind, c.Strict), func(t *testing.T) {
			req, stored, raw, opts, _ := publicRefusalFixture(c)

			pending, err := update.Prepare(raw, req, opts...)
			if err != nil {
				t.Fatal(err)
			}

			if c.Entry == "ApplyMask" {
				_, err = pending.Complete(stored)
			} else {
				spec, setup := update.NewSpec(req, opts...)
				if setup != nil {
					t.Fatal(setup)
				}

				ctx := update.ContextWithPlan(t.Context(), pending)
				if c.Entry == "Spec" {
					_, err = spec.Plan(req, pending.Mask().ToProtoFieldMask(), update.WithCurrent(stored), update.WithPlanContext(ctx))
				} else {
					patch := refusalPatch(spec, stored)
					_, err = patch.Run(ctx, req, "", pending.Mask().ToProtoFieldMask())
				}
			}

			if err == nil {
				t.Fatal("deferred corpus input must refuse")
			}

			mapped := mapper.Map(err)
			assertOrderedRefusal(t, c, record, err, int32(mapped.Code()), refusalFields(mapped))
		})
	}
}
