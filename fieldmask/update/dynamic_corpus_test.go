// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
)

// The corpus helpers use only a8b17ba APIs. testdata/record_dynamic.py runs
// them against an isolated git archive of that revision, never this checkout's
// production sources or dependencies. No test suite is needed to record data.
type dynamicTesting interface {
	Helper()
	Fatal(...any)
}

type dynamicCase struct {
	Name          string
	Request       proto.Message `json:"-"`
	Stored        proto.Message `json:"-"`
	Mask          *fieldmaskpb.FieldMask
	Annotated     bool
	MaxPathDepth  int
	Reconstructed bool
}
type dynamicOutcome struct {
	Error      string   `json:",omitempty"`
	Panic      string   `json:",omitempty"`
	Paths      []string `json:",omitempty"`
	Request    string
	MergeError string `json:",omitempty"`
	Merged     string `json:",omitempty"`
}
type dynamicRecord struct {
	Name          string
	Type          string
	Dynamic       bool
	Mask          *fieldmaskpb.FieldMask
	Request       string
	Stored        string
	Outcomes      map[string]dynamicOutcome
	Annotated     bool
	MaxPathDepth  int
	Reconstructed bool `json:",omitempty"`
}

var dynamicEntries = []string{"ApplyMask", "ApplyMask/current", "Apply", "Apply/current", "ValidateStrict", "ValidateStrict/current", "Spec", "Spec/current", "StripUpdate", "StripUpdate/current", "StripUpdate/depth1", "StripUpdate/current/depth1", "StripCreate", "StripResponse"}

func dynamicOptions() []update.Option {
	// The ordinary generated wrapper also contains annotated fixture fields.
	// Disable all behaviors for this corpus: it exercises only mutable data,
	// so protected-field changes cannot excuse a differential failure.
	return []update.Option{update.WithIdentityFields(), update.WithResolver(fieldbehavior.ResolverFunc(func(protoreflect.FieldDescriptor) []annotations.FieldBehavior { return nil }))}
}

func dynamicWire(t dynamicTesting, m proto.Message) string {
	t.Helper()

	b, err := (proto.MarshalOptions{Deterministic: true}).Marshal(m)
	if err != nil {
		t.Fatal(err)
	}

	return base64.StdEncoding.EncodeToString(b)
}

func dynamicForm(t dynamicTesting, m proto.Message, dynamic bool) proto.Message {
	t.Helper()

	if !dynamic {
		return proto.Clone(m)
	}

	out := dynamicpb.NewMessage(m.ProtoReflect().Descriptor())

	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}

	if err := proto.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}

	return out
}

func dynamicCases(t dynamicTesting) []dynamicCase {
	t.Helper()

	rng := rand.New(rand.NewPCG(203, 9))
	objects := []*structpb.Struct{{}, {Fields: map[string]*structpb.Value{"color": structpb.NewStringValue("blue")}},
		{Fields: map[string]*structpb.Value{"color": structpb.NewStringValue("red"), "keep": structpb.NewBoolValue(true)}},
		{Fields: map[string]*structpb.Value{"a.b": structpb.NewNumberValue(2), "fields": structpb.NewStringValue("json"), "values": structpb.NewBoolValue(true)}},
	}
	objects = append(objects, &structpb.Struct{Fields: map[string]*structpb.Value{"work": structpb.NewStructValue(objects[1]), "items": structpb.NewListValue(&structpb.ListValue{Values: []*structpb.Value{structpb.NewStructValue(objects[2])}})}})
	values := []*structpb.Value{{}, structpb.NewNullValue(), structpb.NewNumberValue(1), structpb.NewNumberValue(2), structpb.NewStructValue(objects[1]), structpb.NewStructValue(objects[2]), structpb.NewListValue(&structpb.ListValue{Values: []*structpb.Value{structpb.NewNumberValue(2)}})}
	lists := []*structpb.ListValue{{}, {Values: []*structpb.Value{structpb.NewNumberValue(1)}}, {Values: []*structpb.Value{structpb.NewNumberValue(2)}}, {Values: []*structpb.Value{structpb.NewStructValue(objects[1]), structpb.NewStructValue(objects[2])}}}
	roots := func(kind, n int) proto.Message {
		switch kind {
		case 0:
			return objects[n%len(objects)]
		case 1:
			return values[n%len(values)]
		case 2:
			return lists[n%len(lists)]
		case 3:
			return &pb.Options{Color: new(fmt.Sprintf("color%d", n)), Size: new(int32(n % 3))}
		default:
			if n%7 == 0 {
				return &pb.UpdateRequest{}
			}

			return &pb.UpdateRequest{Description: new(fmt.Sprintf("description%d", n)), ExtraData: objects[n%len(objects)], ValueData: values[n%len(values)], ListData: lists[n%len(lists)], StructList: []*structpb.Struct{objects[n%len(objects)]}, StructMap: map[string]*structpb.Struct{"k": objects[n%len(objects)]}, Options: &pb.Options{Color: new("green")}}
		}
	}
	masks := [][]string{nil, {}, {"*"}, {"color"}, {"work.color"}, {"items.*.color"}, {"`a.b`"}, {"fields"}, {"values"}, {"missing"}, {"*.color"}, {"color", "work"}, {"work.*"}, {"0"}, {"color..x"}}
	nested := [][]string{nil, {}, {"*"}, {"extra_data"}, {"extraData.color"}, {"extra_data.work.color"}, {"extra_data.items.*.color"}, {"extra_data.*"}, {"value_data.color"}, {"list_data.*.color"}, {"struct_list.*.color"}, {"struct_map.k.color"}, {"struct_map.*.color"}, {"extra_data.`a.b`"}, {"options.color"}, {"description"}, {"list_data.*"}, {"value_data"}}

	var cases []dynamicCase

	for i := range 180 {
		kind := i % 5

		// Each root kind meets every selection kind; payloads remain seeded.
		paths := masks[(i/5)%len(masks)]
		if kind == 4 {
			paths = nested[(i/5)%len(nested)]
		}

		req, stored := roots(kind, rng.IntN(35)), roots(kind, rng.IntN(35))

		var raw *fieldmaskpb.FieldMask
		if paths != nil {
			raw = &fieldmaskpb.FieldMask{Paths: paths}
		}

		for _, dynamic := range []bool{false, true} {
			cases = append(cases, dynamicCase{Name: fmt.Sprintf("%04d/kind%d/dynamic%t", i, kind, dynamic), Request: dynamicForm(t, req, dynamic), Stored: dynamicForm(t, stored, dynamic), Mask: raw})
		}
	}

	cases = append(cases, collisionCases(t)...)
	cases = append(cases, deepCases(t)...)
	cases = append(cases, reconstructedCases(t)...)

	return cases
}

func dynamicError(err error) string {
	if err == nil {
		return ""
	}

	for _, s := range []struct {
		name string
		err  error
	}{
		{"depth", fieldbehavior.ErrMaxDepthExceeded}, {"strip-behavior", fieldbehavior.ErrBehaviorViolation}, {"invalid-path", fieldmask.ErrInvalidPath}, {"element-count", update.ErrElementCountMismatch}, {"message-mismatch", update.ErrMessageMismatch},
	} {
		if errors.Is(err, s.err) {
			return s.name
		}
	}
	// Keep an unknown error exact; never silently treat new failures as equal.
	return fmt.Sprintf("%T: %v", err, err)
}

func dynamicRun(t dynamicTesting, c dynamicCase, entry string) (out dynamicOutcome) {
	t.Helper()

	req, stored := proto.Clone(c.Request), proto.Clone(c.Stored)

	defer func() {
		if p := recover(); p != nil {
			out.Panic = fmt.Sprint(p)
		}

		out.Request = dynamicWire(t, req)
	}()

	opts := dynamicCaseOptions(c)
	if entry == "MergePaths" {
		out.Paths = append([]string(nil), c.Mask.GetPaths()...)
		out.MergeError = dynamicError(update.MergePaths(stored, req, out.Paths, opts...))
		out.Merged = dynamicWire(t, stored)

		return out
	}

	if strings.HasPrefix(entry, "Strip") {
		var stripOpts []fieldbehavior.Option
		if !c.Annotated {
			stripOpts = append(stripOpts, fieldbehavior.WithResolver(fieldbehavior.ResolverFunc(func(protoreflect.FieldDescriptor) []annotations.FieldBehavior { return nil })))
		}

		if strings.Contains(entry, "/current") {
			stripOpts = append(stripOpts, fieldbehavior.WithCurrent(stored))
		}

		if strings.HasSuffix(entry, "/depth1") {
			stripOpts = append(stripOpts, fieldbehavior.WithMaxDepth(1))
		}

		var err error

		switch strings.Split(entry, "/")[0] {
		case "StripUpdate":
			err = fieldbehavior.StripUpdate(req, stripOpts...)
		case "StripCreate":
			err = fieldbehavior.StripCreate(req, stripOpts...)
		case "StripResponse":
			err = fieldbehavior.StripResponse(req, stripOpts...)
		}

		out.Error = dynamicError(err)

		return out
	}

	known := false
	for _, candidate := range []string{"ApplyMask/current", "Apply/current", "ValidateStrict/current", "Spec/current"} {
		known = known || entry == candidate
	}

	if known {
		opts = append(opts, update.WithCurrent(stored))
	}

	m := fieldmask.FromPaths(c.Mask.GetPaths()...)
	if c.Mask == nil {
		m = nil
	}

	var err error

	switch entry {
	case "ApplyMask", "ApplyMask/current":
		m, err = update.ApplyMask(c.Mask, req, opts...)
	case "Apply", "Apply/current":
		err = update.Apply(m, req, opts...)
	case "ValidateStrict", "ValidateStrict/current":
		m, err = update.ValidateStrict(m, req, opts...)
	case "Spec", "Spec/current":
		spec, e := update.NewSpec(req, dynamicCaseOptions(c)...)
		if e != nil {
			t.Fatal(e)
		}

		p, e := spec.Plan(req, c.Mask, opts...)
		m, err = fieldmask.FromPaths(p.Paths...), e
	}

	out.Error = dynamicError(err)
	if err == nil {
		out.Paths = append([]string(nil), m.ToPaths()...)
		// Apply's nil map cannot return an implied mask; preserve that API's
		// exact outcome, including the no-op merge by its caller-owned map.
		out.MergeError = dynamicError(update.MergePaths(stored, req, out.Paths, dynamicCaseOptions(c)...))
		out.Merged = dynamicWire(t, stored)
	}

	return out
}

func TestRecordDynamicCorpus(t *testing.T) {
	path := os.Getenv("AIP_DYNAMIC_GOLDEN")
	if path == "" {
		t.Skip("base-only golden generator")
	}

	output, err := exec.CommandContext(t.Context(), "python3", "testdata/record_dynamic.py", path).CombinedOutput()
	if err != nil {
		t.Fatalf("base recorder: %v\n%s", err, output)
	}

	t.Log(string(output))
}

func readDynamicCorpus(t *testing.T) []dynamicRecord {
	t.Helper()

	packed, err := os.ReadFile("testdata/dynamic_base.jsonl.gz")
	if err != nil {
		t.Fatal(err)
	}

	const goldenSHA256 = "280387769f08560c575b649b6d715c6b3d64e98e7e729994ae5afce6dd1c7229"
	if got := fmt.Sprintf("%x", sha256.Sum256(packed)); got != goldenSHA256 {
		t.Fatalf("base golden checksum %s; want %s", got, goldenSHA256)
	}

	zr, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		t.Fatal(err)
	}

	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}

	var records []dynamicRecord

	decoder := json.NewDecoder(bytes.NewReader(b))

	for {
		var record dynamicRecord
		if err := decoder.Decode(&record); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			t.Fatal(err)
		}

		records = append(records, record)
	}

	const (
		corpusSize  = 638
		inputSHA256 = "90491411d2729c0aa508b26092004c83a3685853b53d9e998ab1b168a4d0ee4e"
	)
	if got := dynamicInputChecksum(records); len(records) != corpusSize || got != inputSHA256 {
		t.Fatalf("corpus size=%d input checksum=%s; want size=%d checksum=%s", len(records), got, corpusSize, inputSHA256)
	}

	return records
}

// Assert the recorded inputs too: changing the seed or fixture distribution
// must require deliberate regeneration on the base, never silently retarget it.
func checkDynamicInput(t *testing.T, c dynamicCase, r dynamicRecord) {
	t.Helper()

	_, dynamic := c.Request.(*dynamicpb.Message)
	if c.Reconstructed != r.Reconstructed || c.Annotated != r.Annotated || c.MaxPathDepth != r.MaxPathDepth || c.Name != r.Name || dynamic != r.Dynamic || string(c.Request.ProtoReflect().Descriptor().FullName()) != r.Type ||
		dynamicWire(t, c.Request) != r.Request || dynamicWire(t, c.Stored) != r.Stored || !proto.Equal(c.Mask, r.Mask) {
		t.Fatalf("corpus input changed: %s", c.Name)
	}
}

// The options are inputs too: annotated cases retain the real resolver so an
// irrelevant mutable branch beside protected fields cannot hide a regression.
func dynamicCaseOptions(c dynamicCase) []update.Option {
	opts := dynamicOptions()
	if c.Annotated {
		opts = []update.Option{update.WithIdentityFields()}
	}

	if c.MaxPathDepth > 0 {
		opts = append(opts, update.WithMaxPathDepth(c.MaxPathDepth))
	}

	return opts
}

// Pin both the deterministic inputs and the base-produced data. The recorder
// runs only production sources and dependencies archived from a8b17ba.
func dynamicInputChecksum(records []dynamicRecord) string {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)

	for _, r := range records {
		r.Outcomes = nil
		if err := encoder.Encode(r); err != nil {
			panic(err)
		}
	}

	return fmt.Sprintf("%x", hash.Sum(nil))
}
