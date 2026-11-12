#!/usr/bin/env python3
"""Record public refusals against the pinned base; never run the test suite."""
import io
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile

BASE = "a8b17ba004770ba389f036a82317f85b5625ef16"
ROOT = Path(__file__).resolve().parents[3]
OUTPUT = ROOT / "apierror/aipmap/testdata/refusals_base.jsonl.gz"
GENERATED_OUTPUT = ROOT / "apierror/aipmap/testdata/refusals_generated_base.jsonl.gz"
RECORDER = r'''package main
import (
 "compress/gzip"
 "context"
 "encoding/json"
 "errors"
 "os"
 "slices"
 "google.golang.org/protobuf/proto"
 "github.com/altessa-s/go-aip/apierror"
 "github.com/altessa-s/go-aip/apierror/aipmap"
 "github.com/altessa-s/go-aip/fieldmask/update"
)
func writeRecords(path string, record func(*json.Encoder)) {
 f, err := os.Create(path)
 if err != nil { panic(err) }
 zw := gzip.NewWriter(f)
 record(json.NewEncoder(zw))
 if err := zw.Close(); err != nil { panic(err) }
 if err := f.Close(); err != nil { panic(err) }
}
func main() {
 mapper, err := apierror.NewMapper(apierror.WithRules(aipmap.Rules()...))
 if err != nil { panic(err) }
 writeRecords(os.Args[1], func(enc *json.Encoder) {
  for _, c := range publicRefusalCases() {
   refusal := publicRefusal(c)
   for _, seq := range baseSequences() {
    r := refusalInput(c)
    r.Shape = refusalTree(refusal)
    r.Sequence = seq
    if refusal != nil {
     mapped := mapper.Map(baseWrap(refusal, seq))
     r.Code, r.Fields = int32(mapped.Code()), refusalFields(mapped)
    }
    if err := enc.Encode(r); err != nil { panic(err) }
   }
  }
 })
 writeRecords(os.Args[2], func(enc *json.Encoder) {
  for _, c := range generatedRefusalCases() {
   req, stored, raw, opts := generatedRefusalFixture(c)
   r := generatedRefusalRecord{Case: c, Request: generatedRefusalWire(req), Stored: generatedRefusalWire(stored)}
   _, err := update.ApplyMask(raw, proto.Clone(req), append(slices.Clone(opts), update.WithCurrent(stored))...)
   if err != nil {
    var violation *update.BehaviorError
    if c.Layout == "indexed-list" {
     shape := refusalTree(err)
     r.Rejection = &shape
    } else {
     if !errors.As(err, &violation) { panic(err) }
     r.Apply = violation.Violations
    }
   }
   r.Order = generatedRefusalOrder(c, r.Apply)
   spec, err := update.NewSpec(req, opts...)
   if err != nil { panic(err) }
   _, err = spec.Plan(proto.Clone(req), raw, update.WithCurrent(stored))
   r.Spec = refusalTree(err)
   patch := refusalPatch(spec, stored)
   _, err = patch.Run(context.Background(), proto.Clone(req), "", raw)
   r.Patch = refusalTree(err)
   if err := enc.Encode(r); err != nil { panic(err) }
  }
 })
}
'''

with tempfile.TemporaryDirectory(prefix="aip-refusal-base-") as directory:
    archive = subprocess.check_output(["git", "archive", BASE], cwd=ROOT)
    with tarfile.open(fileobj=io.BytesIO(archive)) as source:
        source.extractall(directory, filter="data")
    recorder = Path(directory) / "refusalrecorder"
    recorder.mkdir()
    for name in ("public_refusal_corpus_test.go", "refusal_parity_helpers_test.go", "refusal_generated_corpus_test.go"):
        text = (ROOT / "apierror/aipmap" / name).read_text()
        (recorder / name.replace("_test", "")).write_text(text.replace("package aipmap_test", "package main", 1))
    (recorder / "main.go").write_text(RECORDER)
    environment = dict(os.environ)
    environment["GOWORK"] = "off"
    environment["GOFLAGS"] = ""
    environment.setdefault("GOCACHE", str(Path(directory) / "gocache"))
    subprocess.run(["go", "run", "./refusalrecorder", str(OUTPUT), str(GENERATED_OUTPUT)], cwd=directory, env=environment, check=True)
