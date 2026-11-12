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
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// The gzip was recorded exclusively from public calls in an archive of
// a8b17ba004770ba389f036a82317f85b5625ef16. testdata/record_refusals.py regenerates
// it without running tests. The input messages, raw paths, wrappers, type tree,
// Is identities and mapped paths are all pinned, including accepted base calls.
func readRefusalParity(t *testing.T) []refusalRecord {
	t.Helper()

	packed, err := os.ReadFile("testdata/refusals_base.jsonl.gz")
	if err != nil {
		t.Fatal(err)
	}

	const wantSHA256 = "204e1481cb4f3d765cd19df57ef759b31fc79e9f92d89d95ced0b7086452eae5"
	if got := fmt.Sprintf("%x", sha256.Sum256(packed)); got != wantSHA256 {
		t.Fatalf("base corpus checksum = %s, want %s", got, wantSHA256)
	}

	zr, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	var records []refusalRecord

	decoder := json.NewDecoder(zr)

	for {
		var record refusalRecord
		if err := decoder.Decode(&record); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			t.Fatal(err)
		}

		records = append(records, record)
	}

	return records
}

func TestPublicRefusalBaseParity(t *testing.T) {
	cases, sequences, records := publicRefusalCases(), baseSequences(), readRefusalParity(t)
	if len(records) != len(cases)*len(sequences) {
		t.Fatalf("corpus length = %d, want %d", len(records), len(cases)*len(sequences))
	}

	mapper := newMapper(t)
	inputs := make([]refusalRecord, len(cases))
	refusals := make([]error, len(cases))

	for i, c := range cases {
		inputs[i], refusals[i] = refusalInput(c), publicRefusal(c)
	}

	for i, record := range records {
		c, seq := cases[i/len(sequences)], sequences[i%len(sequences)]

		input := inputs[i/len(sequences)]
		if record.Case != c || !slices.Equal(record.Sequence, seq) || record.Request != input.Request ||
			record.Stored != input.Stored || !slices.Equal(record.Raw, input.Raw) {
			t.Fatalf("input %d differs from base recording", i)
		}

		t.Run(fmt.Sprintf("%s/%s/strict=%t/%v", c.Entry, c.Kind, c.Strict, seq), func(t *testing.T) {
			err := refusals[i/len(sequences)]
			if err == nil {
				t.Fatal("corpus input must refuse at HEAD")
			}

			mapped := mapper.Map(baseWrap(err, seq))
			if strings.HasPrefix(c.Kind, "order/") {
				assertOrderedRefusal(t, c, record, err, int32(mapped.Code()), refusalFields(mapped))
				return
			}

			if path, changed := newProtectedRefusal(c); changed {
				// The base accepted collection replacements and StripUpdate oneof
				// clears. Other oneof calls already refused the selected member;
				// the retained merge-effect rule now diagnoses its cleared sibling.
				assertNewProtectedShape(t, c, err, path)

				want := protectedRefusalPaths(c, seq, path)
				if int32(mapped.Code()) != 3 || !slices.Equal(refusalFields(mapped), want) {
					t.Fatalf("code=%v paths=%v, want INVALID_ARGUMENT %v", mapped.Code(), refusalFields(mapped), want)
				}

				return
			}

			if c.Kind == "dynamic-wildcard" {
				// The base accepted this dynamic-root wildcard combination.
				// The branch's grammar refuses it as MergePaths path validation.
				ve, ok := err.(*fieldmask.ValidationError)
				if !ok || ve.Path != "*" || ve.Cause != nil {
					t.Fatalf("want base-shaped validation error on *, got %#v", err)
				}

				want := validationRefusalPaths(seq)
				if int32(mapped.Code()) != 3 || !slices.Equal(refusalFields(mapped), want) {
					t.Fatalf("code=%v paths=%v, want INVALID_ARGUMENT %v", mapped.Code(), refusalFields(mapped), want)
				}

				return
			}

			if c.Kind == "elements" && c.Entry != "Spec" && c.Entry != "Patch" && c.Entry != "MergePaths" {
				assertNewCountShape(t, c, err)

				wantCode := int32(9)

				var want []string
				if c.Entry == "ValidateStrict" || (c.Entry == "ApplyMask" && c.Strict) {
					wantCode, want = 3, aggregateRefusalPaths(seq, "updateMask")
				}

				if int32(mapped.Code()) != wantCode || !slices.Equal(refusalFields(mapped), want) {
					t.Fatalf("count refusal: code=%v paths=%v, want %d %v", mapped.Code(), refusalFields(mapped), wantCode, want)
				}

				return
			}

			if !reflect.DeepEqual(refusalTree(err), record.Shape) {
				t.Errorf("error tree differs from base:\ngot  %#v\nwant %#v", refusalTree(err), record.Shape)
			}

			if int32(mapped.Code()) != record.Code || !slices.Equal(refusalFields(mapped), record.Fields) {
				t.Errorf("code=%v fields=%v, base code=%d fields=%v", mapped.Code(), refusalFields(mapped), record.Code, record.Fields)
			}
		})
	}
}

// These are literal resource paths determined from the public fixtures, never
// extracted from the error under test. The oneof case is an explicit semantic
// deviation: the old refusal named invoice_email, the protected clear names card.
func newProtectedRefusal(c publicRefusalCase) (string, bool) {
	switch c.Kind {
	case "collection", "removed", "element":
		return "members.0.org", true
	case "map", "map-element":
		return "delegates.`a.b`.org", true
	case "oneof":
		return "card", true
	default:
		return "", false
	}
}

func assertNewProtectedShape(t *testing.T, c publicRefusalCase, err error, path string) {
	t.Helper()

	v := update.FieldViolation{Path: path, Behavior: annotations.FieldBehavior_IMMUTABLE,
		Reason: "immutable field cannot be changed from its stored value"}

	switch c.Entry {
	case "Spec", "Patch":
		field := "updateMask.paths[0]"

		index, raw := 0, "members"
		if c.Kind == "map" {
			raw = "delegates"
		}

		if c.Kind == "element" {
			raw = "members.*.org"
		}

		if c.Kind == "map-element" {
			raw = "delegates.*.org"
		}

		if c.Kind == "oneof" {
			field, index, raw = "updateMask", -1, ""
		}

		fe, ok := err.(*coreerrs.FieldError)
		if !ok {
			t.Fatalf("want FieldError, got %T", err)
		}

		if c.Entry == "Patch" {
			if fe.Field != "resource."+path {
				t.Fatalf("Patch field = %q", fe.Field)
			}

			fe, ok = fe.Err.(*coreerrs.FieldError)
			if !ok {
				t.Fatalf("Patch cause = %T, want Spec FieldError", err)
			}
		}

		mp, ok := fe.Err.(*update.MaskPathError)
		if !ok || fe.Field != field || mp.Index != index || mp.Path != raw || mp.Field != path ||
			fmt.Sprintf("%T", mp.Err) != "*update.changedError" || errors.Unwrap(mp.Err) != update.ErrProtectedField {
			t.Fatalf("wrong base-shaped protected refusal: %#v / %#v", fe, mp)
		}

		if _, ok := errors.AsType[*update.BehaviorError](err); ok {
			t.Fatal("Plan must not retain an aggregate")
		}

		if errors.Is(err, update.ErrBehaviorViolation) || errors.Is(err, update.ErrInvalidUpdateMask) {
			t.Fatal("Plan must retain only its base identities")
		}
	case "StripUpdate":
		be, ok := err.(*fieldbehavior.BehaviorViolationError)
		if !ok || len(be.Violations) != 1 || be.Violations[0].Path != path || be.Violations[0].Behavior != v.Behavior {
			t.Fatalf("wrong StripUpdate aggregate: %#v", err)
		}
	default:
		if c.Entry == "ValidateStrict" || (c.Entry == "ApplyMask" && c.Strict) {
			se, ok := err.(*update.StrictError)
			if !ok || se.Cause != nil || !slices.Equal(se.Violations, []update.FieldViolation{v}) {
				t.Fatalf("wrong strict aggregate: %#v", err)
			}
		} else {
			be, ok := err.(*update.BehaviorError)
			if !ok || !slices.Equal(be.Violations, []update.FieldViolation{v}) || errors.Unwrap(be) != update.ErrBehaviorViolation {
				t.Fatalf("wrong behavior aggregate: %#v", err)
			}
		}
	}
}

func assertNewCountShape(t *testing.T, c publicRefusalCase, err error) {
	t.Helper()

	if c.Entry == "ValidateStrict" || (c.Entry == "ApplyMask" && c.Strict) {
		se, ok := err.(*update.StrictError)
		if !ok || len(se.Violations) != 1 || se.Violations[0].Path != "updateMask" {
			t.Fatalf("wrong strict count refusal: %#v", err)
		}

		err = se.Cause
	}

	if fmt.Sprintf("%T", err) != "*update.elementCountError" || errors.Unwrap(err) != update.ErrElementCountMismatch {
		t.Fatalf("wrong count refusal: %#v", err)
	}
}

// Literal wrapper behavior recorded at the base for aggregate errors. In
// particular a batch does not prefix a bare aggregate; an enclosing FieldError
// does, and a batch outside that FieldError prefixes its selected field.
func aggregateRefusalPaths(seq []int, path string) []string {
	if slices.Equal(seq, []int{15, 2}) {
		return []string{"requests[2].container." + path}
	}

	if slices.Contains(seq, 15) {
		return []string{"container." + path}
	}

	if slices.Contains(seq, 16) || slices.Contains(seq, 18) {
		return []string{"book.uid"}
	}

	return []string{path}
}

func protectedRefusalPaths(c publicRefusalCase, seq []int, path string) []string {
	if c.Entry != "Spec" && c.Entry != "Patch" {
		return aggregateRefusalPaths(seq, path)
	}

	field := "updateMask.paths[0]"
	if c.Kind == "oneof" {
		field = "updateMask"
	}

	if c.Entry == "Patch" {
		field = "resource." + path
	}

	return wrappedPlanRefusalPaths(seq, field)
}

func wrappedPlanRefusalPaths(seq []int, field string) []string {
	for _, wrap := range seq {
		switch wrap {
		case 0:
			field = "mapped." + field
		case 1:
			field = "renamed[" + strings.ReplaceAll(field, ".", "/") + "]"
		case 2:
			field = "requests[2]." + field
		case 3, 4, 5:
			field = "requests[0]." + field
		case 12, 13:
			field = "other"
		case 15:
			field = "container"
		case 16, 18:
			field = "book.uid"
		case 17:
			field = "a"
		}
	}

	return []string{field}
}

// Every refusal in the public corpus checks Prepare's stateful parity. The
// stateless stage may itself refuse invalid input; only successful preparation
// can supply a plan to Complete, Spec or Patch. Such early errors are compared
// with ApplyMask, the entry point whose contract Prepare implements.
func TestPublicRefusalTwoStageParity(t *testing.T) {
	for _, c := range publicRefusalCases() {
		if c.Entry == "MergePaths" || c.Entry == "StripUpdate" || c.Entry == "Apply" || c.Entry == "ValidateStrict" {
			continue
		}

		t.Run(fmt.Sprintf("%s/%s/strict=%t", c.Entry, c.Kind, c.Strict), func(t *testing.T) {
			req, stored, raw, opts, _ := publicRefusalFixture(c)
			_, directApply := update.ApplyMask(raw, proto.Clone(req), append(slices.Clone(opts), update.WithCurrent(stored))...)
			_, statefulPrepare := update.Prepare(raw, proto.Clone(req), append(slices.Clone(opts), update.WithCurrent(stored))...)
			assertRefusalParity(t, directApply, statefulPrepare)

			original := proto.Clone(req)

			pending, early := update.Prepare(raw, req, opts...)
			if early != nil {
				_, stateless := update.ApplyMask(raw, original, opts...)
				assertRefusalParity(t, stateless, early)

				if c.Kind == "mixed-required" {
					// No plan exists after REQUIRED fails. Without stored state
					// Prepare cannot also report the changed immutable name.
					if reflect.DeepEqual(refusalTree(directApply), refusalTree(early)) {
						t.Fatal("fixture must expose the early-refusal equivalence limitation")
					}
				} else {
					assertRefusalParity(t, directApply, early)
				}

				return
			}

			_, complete := pending.Complete(stored)
			assertRefusalParity(t, directApply, complete)

			_, repeated := pending.Complete(stored)
			assertRefusalParity(t, complete, repeated)

			if c.Entry == "ApplyMask" {
				return
			}

			spec, err := update.NewSpec(req, opts...)
			if err != nil {
				t.Fatal(err)
			}

			ctx := update.ContextWithPlan(t.Context(), pending)

			var deferred error
			if c.Entry == "Spec" {
				_, deferred = spec.Plan(req, pending.Mask().ToProtoFieldMask(), update.WithCurrent(stored), update.WithPlanContext(ctx))
			} else {
				patch := refusalPatch(spec, stored)
				_, deferred = patch.Run(ctx, req, "", pending.Mask().ToProtoFieldMask())
			}

			assertRefusalParity(t, publicRefusal(c), deferred)
		})
	}
}

func assertRefusalParity(t *testing.T, direct, deferred error) {
	t.Helper()

	if direct == nil || deferred == nil || !reflect.DeepEqual(refusalTree(direct), refusalTree(deferred)) {
		t.Fatalf("one/two-stage shape mismatch:\n%#v\n%#v", refusalTree(direct), refusalTree(deferred))
	}

	mapper := newMapper(t)
	for _, seq := range baseSequences() {
		one, two := mapper.Map(baseWrap(direct, seq)), mapper.Map(baseWrap(deferred, seq))
		if one.Code() != two.Code() || !slices.Equal(refusalFields(one), refusalFields(two)) {
			t.Fatalf("one/two-stage mapping differs under %v: %v / %v", seq, refusalFields(one), refusalFields(two))
		}
	}
}

func TestSupersededReviewInputsUseBaseSelection(t *testing.T) {
	_, change := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: []string{"name"}},
		&descriptorpb.FileDescriptorProto{Name: new("new")}, update.WithImmutableFields("name"),
		update.WithCurrent(&descriptorpb.FileDescriptorProto{Name: new("old")}))
	if _, ok := change.(*update.BehaviorError); !ok {
		t.Fatalf("want base BehaviorError, got %T", change)
	}

	mapper := newMapper(t)
	for _, tc := range []struct {
		name     string
		sequence []int
		want     []string
	}{
		{"R21-01", []int{17}, []string{"name"}},
		{"R21-02", []int{18}, []string{"book.uid"}},
		{"R21-02/composed", []int{18, 0, 1}, []string{"book.uid"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := refusalFields(mapper.Map(baseWrap(change, tc.sequence))); !slices.Equal(got, tc.want) {
				t.Fatalf("fields = %v, base = %v", got, tc.want)
			}
		})
	}
}

// A boundary policy can forbid an operation the service's Spec allows. Keeping
// that existing policy enforcement necessarily differs from planning without
// the boundary plan, but the refusal still uses the base Spec/Patch construction.
func TestDeferredBoundaryPolicyRefusal(t *testing.T) {
	for _, strict := range []bool{false, true} {
		for _, patch := range []bool{false, true} {
			t.Run(fmt.Sprintf("strict=%t/patch=%t", strict, patch), func(t *testing.T) {
				req := &descriptorpb.FileDescriptorProto{Name: new("new")}
				stored := &descriptorpb.FileDescriptorProto{Name: new("old")}
				raw := &fieldmaskpb.FieldMask{Paths: []string{"name"}}

				spec, err := update.NewSpec(req)
				if err != nil {
					t.Fatal(err)
				}

				if _, err := spec.Plan(proto.Clone(req), raw, update.WithCurrent(stored)); err != nil {
					t.Fatalf("Spec alone permits the boundary-protected field: %v", err)
				}

				pending, err := update.Prepare(raw, req, update.WithImmutableFields("name"), update.WithStrict(strict))
				if err != nil {
					t.Fatal(err)
				}

				ctx := update.ContextWithPlan(t.Context(), pending)

				if patch {
					p := refusalPatch(spec, stored)
					_, err = p.Run(ctx, req, "", pending.Mask().ToProtoFieldMask())
				} else {
					_, err = spec.Plan(req, pending.Mask().ToProtoFieldMask(), update.WithCurrent(stored), update.WithPlanContext(ctx))
				}

				fe, ok := err.(*coreerrs.FieldError)
				if !ok || !errors.Is(err, update.ErrProtectedField) || errors.Is(err, update.ErrBehaviorViolation) ||
					errors.Is(err, update.ErrInvalidUpdateMask) {
					t.Fatalf("boundary refusal lost the base plan shape: %#v", err)
				}

				if patch {
					if fe.Field != "resource.name" {
						t.Fatalf("Patch field = %q", fe.Field)
					}

					fe, ok = fe.Err.(*coreerrs.FieldError)
					if !ok {
						t.Fatalf("Patch must retain its Spec FieldError: %v", err)
					}
				}

				mp, ok := fe.Err.(*update.MaskPathError)
				if !ok || fe.Field != "updateMask.paths[0]" || mp.Field != "name" || mp.Index != 0 || mp.Path != "name" ||
					fmt.Sprintf("%T", mp.Err) != "*update.changedError" || errors.Unwrap(mp.Err) != update.ErrProtectedField {
					t.Fatalf("boundary Spec refusal = %#v / %#v", fe, mp)
				}
			})
		}
	}
}

func validationRefusalPaths(seq []int) []string {
	if slices.Equal(seq, []int{15, 2}) {
		return []string{"requests[2].container"}
	}

	if slices.Contains(seq, 15) {
		return []string{"container"}
	}

	if len(seq) == 1 {
		switch seq[0] {
		case 12, 13, 14:
			return []string{"other"}
		case 16, 18:
			return []string{"book.uid"}
		case 17:
			return []string{"a"}
		}
	}

	return nil
}
