// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"encoding/base64"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
)

// TestDynamicDifferential retains every base result. Exceptions below derive
// exact outcomes from the input or the generated counterpart's base outcome;
// none records HEAD as its own oracle, skips a case, or ignores a result field.
func TestDynamicDifferential(t *testing.T) {
	cases, records := dynamicCases(t), readDynamicCorpus(t)
	if len(cases) != len(records) {
		t.Fatalf("case count %d; golden %d", len(cases), len(records))
	}

	exceptions := map[string]int{}

	for i, c := range cases {
		reference := i &^ 1
		if strings.HasPrefix(c.Name, "deep") {
			reference = i // descriptor-only cases are recorded once
		}

		t.Run(c.Name, func(t *testing.T) {
			checkDynamicInput(t, c, records[i])

			for _, entry := range dynamicCaseEntries(c) {
				want, reasons := dynamicExpected(t, cases[reference], records[i], records[reference], entry)

				base := records[i].Outcomes[entry]
				if !reflect.DeepEqual(want, base) {
					if len(reasons) == 0 {
						t.Fatal("unexplained baseline exception")
					}

					for _, reason := range reasons {
						exceptions[reason]++
					}
				}

				got := dynamicRun(t, c, entry)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s: got %+v; want %+v; base %+v; exceptions %v", entry, got, want, base, reasons)
				}
			}

			want, _ := dynamicExpected(t, cases[reference], records[i], records[reference], "ApplyMask/current")
			req, stored := proto.Clone(c.Request), proto.Clone(c.Stored)

			pending, err := update.Prepare(c.Mask, req, dynamicCaseOptions(c)...)
			if err == nil {
				mask, e := pending.Complete(stored)

				err = e
				if err == nil {
					if !slices.Equal(mask.ToPaths(), want.Paths) {
						t.Errorf("Complete paths %v; want %v", mask.ToPaths(), want.Paths)
					}

					mergeErr := dynamicError(update.MergePaths(stored, req, mask.ToPaths(), dynamicCaseOptions(c)...))
					if mergeErr != want.MergeError || dynamicWire(t, stored) != want.Merged {
						t.Errorf("Complete merge: error=%s value=%s; want %+v", mergeErr, dynamicWire(t, stored), want)
					}
				}
			}

			if dynamicError(err) != want.Error || dynamicWire(t, req) != want.Request {
				t.Errorf("Prepare/Complete: error=%v request=%s; want %+v", err, dynamicWire(t, req), want)
			}

			if dynamicWire(t, c.Stored) != records[i].Stored {
				t.Fatal("stored input changed")
			}
		})
	}

	t.Logf("exact baseline exceptions by reason: %v", exceptions)
}

func dynamicExpected(t *testing.T, generated dynamicCase, record, baseGenerated dynamicRecord, entry string) (dynamicOutcome, []string) {
	t.Helper()

	want := record.Outcomes[entry]
	if entry == "MergePaths" {
		if slices.Equal(record.Mask.GetPaths(), []string{"*"}) {
			return dynamicOutcome{Paths: []string{"*"}, Request: record.Request, Merged: record.Request},
				[]string{"R9-01/AIP-134: dynamic-root full replacement replaces the entire value"}
		}

		// Reconstructed-descriptor writes worked on the clean base. Pin its
		// exact result, including retained destination keys, without substituting
		// the generated counterpart or allowing a descriptor-conversion panic.
		return want, nil
	}

	var reasons []string

	if record.Annotated {
		return expectedDeepCase(t, generated, record, entry)
	}

	if strings.HasPrefix(entry, "Strip") {
		return want, nil
	}

	if len(record.Mask.GetPaths()) == 1 && strings.HasSuffix(record.Mask.Paths[0], "`*`") && !strings.HasPrefix(record.Mask.Paths[0], "*.") {
		return expectedLiteralStar(t, generated, entry), []string{"R10-01: quoted literal JSON keys retain their identity through every mask entry point"}
	}

	if record.Dynamic {
		want = baseGenerated.Outcomes[entry]

		reasons = append(reasons, "R9-02: descriptor-aware dynamic filtering and selection must match generated representation")
	}

	root := strings.HasPrefix(record.Type, "google.protobuf.")

	replacement := slices.Equal(record.Mask.GetPaths(), []string{"*"})
	if root && (record.Mask == nil || replacement) {
		reasons = append(reasons, "R9-01/AIP-134: base dynamic-root omitted masks and full replacements address wrapper fields or no fields")

		paths, merged := expectedDynamicRoot(t, generated, replacement)
		if record.Mask == nil && (entry == "Apply" || entry == "Apply/current") {
			paths = nil
			merged = generated.Stored
		}

		return dynamicOutcome{Paths: paths, Request: record.Request, Merged: dynamicWire(t, merged)}, reasons
	}

	if record.Mask == nil && strings.HasSuffix(record.Type, "UpdateRequest") {
		reasons = append(reasons, "R7/AIP-134: one implied leaf selection; nested dynamic values are whole leaves, not protobuf wrapper paths")
		merged := proto.Clone(generated.Stored)
		paths := expectedImplied(generated.Request.ProtoReflect(), merged.ProtoReflect(), "")
		slices.Sort(paths)

		if entry == "Apply" || entry == "Apply/current" {
			paths = nil
			merged = generated.Stored
		}

		return dynamicOutcome{Paths: paths, Request: record.Request, Merged: dynamicWire(t, merged)}, reasons
	}

	if strings.HasSuffix(entry, "/current") && want.Error == "" && want.MergeError == "element-count" {
		// Round 7 requires checking the exact writer's preconditions before any
		// mutation. The base reports this error only from MergePaths, too late.
		reasons = append(reasons, "R7: merge preconditions are checked during stateful normalization")
		return dynamicOutcome{Error: "element-count", Request: record.Request}, reasons
	}

	if want.Error == "" {
		want.Paths = slices.Clone(want.Paths)
		for i, path := range want.Paths {
			if strings.HasSuffix(path, ".*") {
				reasons = append(reasons, "R7: evaluated and returned masks use the writer's normal form")
				want.Paths[i] = strings.TrimSuffix(path, ".*")
			}
		}
	}

	return want, reasons
}

// expectedDeepCase covers the deliberately simple deep fixtures independently:
// their only mutable branch is a chain ending in value, beside root behaviors.
// Explicit mutable masks still use every base outcome unchanged, except for
// the work-order-1 requirement to retain unverified protected request evidence.
func expectedDeepCase(t *testing.T, c dynamicCase, record dynamicRecord, entry string) (dynamicOutcome, []string) {
	t.Helper()

	want := record.Outcomes[entry]
	req, stored := proto.Clone(c.Request), proto.Clone(c.Stored)
	m := req.ProtoReflect()
	code, output := m.Descriptor().Fields().ByName("code"), m.Descriptor().Fields().ByName("output")
	current := strings.Contains(entry, "/current")

	if strings.HasPrefix(entry, "StripUpdate") && m.Has(code) {
		m.Clear(output)
		return dynamicOutcome{Request: dynamicWire(t, req)}, []string{"WO1/WO2: StripUpdate retains equal or unverified immutable input for subsequent planning"}
	}

	if strings.HasPrefix(entry, "Strip") {
		return want, nil
	}

	if c.Mask != nil {
		var reasons []string

		if len(c.Mask.Paths) > 0 && want.Error == "" && slices.Contains(want.Paths, "name") && (entry == "Apply" || entry == "Apply/current") {
			want.Paths = slices.DeleteFunc(slices.Clone(want.Paths), func(path string) bool { return path == "name" })

			reasons = append(reasons, "WO1: identifiers are retained as routing input, never added to the effective write mask")
		}

		if len(c.Mask.Paths) > 0 && !current && m.Has(code) && want.Error == "" && (entry == "ApplyMask" || entry == "Apply") {
			filtered := dynamicUnwire(t, req, want.Request)
			filtered.ProtoReflect().Set(code, m.Get(code))
			want.Request = dynamicWire(t, filtered)

			reasons = append(reasons, "WO1/WO2: stateless normalization retains unselected protected input as comparison evidence")
		}

		return want, reasons
	}

	if c.MaxPathDepth == 1 && want.Error == "depth" {
		// Selecting tree whole still runs the separate REQUIRED validation
		// walk. Its existing depth refusal is outside protected comparison.
		return want, nil
	}

	limit := c.MaxPathDepth
	if limit == 0 {
		limit = fieldmask.DefaultMaxPathDepth
	}

	path := "tree"

	node := m.Get(m.Descriptor().Fields().ByName("tree")).Message()
	for level := 1; level < limit; level++ {
		child := node.Descriptor().Fields().ByName("child")
		if child == nil || !node.Has(child) {
			path += ".value"
			break
		}

		path += ".child"
		node = node.Get(child).Message()
	}

	paths := []string{"secret", path}
	if strings.HasPrefix(entry, "Spec") {
		paths = []string{path}
	}

	if entry == "Apply" || entry == "Apply/current" {
		paths = nil
	}

	if len(paths) > 0 {
		tree := m.Descriptor().Fields().ByName("tree")
		stored.ProtoReflect().Set(tree, m.Get(tree))

		if !strings.HasPrefix(entry, "Spec") {
			secret := m.Descriptor().Fields().ByName("secret")
			stored.ProtoReflect().Set(secret, m.Get(secret))
		}
	}

	if strings.HasPrefix(entry, "Apply") {
		m.Clear(output)

		if current {
			m.Clear(code)
		}
	}

	var reasons []string

	if !slices.Equal(paths, want.Paths) || want.Error == "depth" {
		reasons = append(reasons, "R7/AIP-134: omitted masks select populated leaves up to the configured path depth; base selected or validated the whole tree")
	}

	if !current && m.Has(code) {
		reasons = append(reasons, "WO1/WO2: supplied immutable input is deferred without stored state")
	}

	return dynamicOutcome{Paths: paths, Request: dynamicWire(t, req), Merged: dynamicWire(t, stored)}, reasons
}

func dynamicUnwire(t *testing.T, template proto.Message, wire string) proto.Message {
	t.Helper()

	b, err := base64.StdEncoding.DecodeString(wire)
	if err != nil {
		t.Fatal(err)
	}

	out := template.ProtoReflect().Type().New().Interface()
	if err := proto.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}

	return out
}

func expectedLiteralStar(t *testing.T, c dynamicCase, entry string) dynamicOutcome {
	t.Helper()

	req, merged := proto.Clone(c.Request), proto.Clone(c.Stored)
	path := c.Mask.Paths[0]
	object := func(m proto.Message) *structpb.Struct {
		switch v := m.(type) {
		case *structpb.Struct:
			if strings.HasPrefix(path, "work.") {
				return v.Fields["work"].GetStructValue()
			}

			return v
		case *structpb.Value:
			return v.GetStructValue()
		case *pb.UpdateRequest:
			return v.ExtraData
		default:
			t.Fatalf("unexpected literal fixture %T", m)
			return nil
		}
	}
	source, target := object(req), object(merged)

	target.Fields["*"] = proto.CloneOf(source.Fields["*"])
	if strings.HasPrefix(entry, "Apply") {
		delete(source.Fields, "color")

		if root, ok := req.(*structpb.Struct); ok && strings.HasPrefix(path, "work.") {
			delete(root.Fields, "keep")
		}
	}

	return dynamicOutcome{Paths: []string{path}, Request: dynamicWire(t, req), Merged: dynamicWire(t, merged)}
}

// expectedDynamicRoot implements only the independent JSON rule for root
// omitted/full masks. It never calls update's selection, filtering or writer.
func expectedDynamicRoot(t *testing.T, c dynamicCase, replacement bool) ([]string, proto.Message) {
	t.Helper()

	if replacement {
		return []string{"*"}, proto.Clone(c.Request)
	}

	var fields map[string]*structpb.Value

	switch m := c.Request.(type) {
	case *structpb.Struct:
		fields = m.GetFields()
	case *structpb.Value:
		switch k := m.Kind.(type) {
		case nil:
			return nil, proto.Clone(c.Stored)
		case *structpb.Value_StructValue:
			fields = k.StructValue.GetFields()
		case *structpb.Value_ListValue:
			if len(k.ListValue.GetValues()) == 0 {
				return nil, proto.Clone(c.Stored)
			}

			return []string{"*"}, proto.Clone(c.Request)
		default:
			return []string{"*"}, proto.Clone(c.Request)
		}
	case *structpb.ListValue:
		if len(m.GetValues()) == 0 {
			return nil, proto.Clone(c.Stored)
		}

		return []string{"*"}, proto.Clone(c.Request)
	default:
		t.Fatalf("not a dynamic root: %T", m)
	}

	merged := proto.Clone(c.Stored)
	if len(fields) == 0 {
		return nil, merged
	}

	var target *structpb.Struct

	switch m := merged.(type) {
	case *structpb.Struct:
		target = m
	case *structpb.Value:
		target = m.GetStructValue()
		if target == nil {
			target = &structpb.Struct{}
			m.Kind = &structpb.Value_StructValue{StructValue: target}
		}
	}

	if target.Fields == nil {
		target.Fields = map[string]*structpb.Value{}
	}

	var paths []string

	for key, value := range fields {
		path := key
		if key == "" || key == "*" || strings.ContainsAny(key, ".`") {
			path = "`" + strings.ReplaceAll(key, "`", "``") + "`"
		}

		paths = append(paths, path)
		target.Fields[key] = proto.CloneOf(value)
	}

	slices.Sort(paths)

	return paths, merged
}

// expectedImplied is a small oracle for the behavior-free ordinary fixture:
// descend ordinary messages, replace populated collections and JSON values.
func expectedImplied(req, dst protoreflect.Message, prefix string) []string {
	var paths []string

	req.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		path := prefix + string(fd.Name())
		if !fd.IsMap() && !fd.IsList() && fd.Message() != nil && !strings.HasPrefix(string(fd.Message().FullName()), "google.protobuf.") {
			paths = append(paths, expectedImplied(v.Message(), dst.Mutable(fd).Message(), path+".")...)
		} else {
			paths = append(paths, path)

			dst.Set(fd, v)
		}

		return true
	})

	return paths
}

func Example_dynamicRootReplacement() {
	request := &structpb.ListValue{Values: []*structpb.Value{structpb.NewNumberValue(2)}}
	stored := &structpb.ListValue{Values: []*structpb.Value{structpb.NewNumberValue(1)}}
	err := update.MergePaths(stored, request, []string{"*"})
	fmt.Println(err, stored.Values[0].GetNumberValue())
	// Output: <nil> 2
}
