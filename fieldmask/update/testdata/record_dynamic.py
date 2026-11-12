#!/usr/bin/env python3
"""Record dynamic outcomes from the pinned clean base without running tests."""
import io
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile

BASE = "a8b17ba004770ba389f036a82317f85b5625ef16"
ROOT = Path(__file__).resolve().parents[3]
OUTPUT = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else ROOT / "fieldmask/update/testdata/dynamic_base.jsonl.gz"
RECORDER = r'''package main
import (
 "bytes"
 "compress/gzip"
 "crypto/sha256"
 "encoding/json"
 "fmt"
 "os"
 "google.golang.org/protobuf/types/dynamicpb"
)
type recorderT struct{}
func (*recorderT) Helper() {}
func (*recorderT) Fatal(args ...any) { panic(fmt.Sprint(args...)) }
func main() { recordDynamicCorpus(&recorderT{}, os.Args[1]) }
func recordDynamicCorpus(t *recorderT, path string) {
	var data bytes.Buffer

	encoder := json.NewEncoder(&data)

	var records []dynamicRecord

	for _, c := range dynamicCases(t) {
		_, dynamic := c.Request.(*dynamicpb.Message)

		r := dynamicRecord{Name: c.Name, Type: string(c.Request.ProtoReflect().Descriptor().FullName()), Dynamic: dynamic, Mask: c.Mask, Request: dynamicWire(t, c.Request), Stored: dynamicWire(t, c.Stored), Outcomes: map[string]dynamicOutcome{}, Annotated: c.Annotated, MaxPathDepth: c.MaxPathDepth, Reconstructed: c.Reconstructed}
		for _, entry := range dynamicCaseEntries(c) {
			r.Outcomes[entry] = dynamicRun(t, c, entry)
		}

		if err := encoder.Encode(r); err != nil {
			t.Fatal(err)
		}

		records = append(records, r)
	}

	fmt.Printf("corpus size=%d input SHA256=%s\n", len(records), dynamicInputChecksum(records))

	// The golden file is stored gzip-compressed: it is a few megabytes of
	// base64 wire data, of no use in a diff.
	var packed bytes.Buffer

	zw := gzip.NewWriter(&packed)
	if _, err := zw.Write(data.Bytes()); err != nil {
		t.Fatal(err)
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, packed.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}

	fmt.Printf("golden SHA256=%x\n", sha256.Sum256(packed.Bytes()))
}

'''

with tempfile.TemporaryDirectory(prefix="aip-dynamic-base-") as directory:
    archive = subprocess.check_output(["git", "archive", BASE], cwd=ROOT)
    with tarfile.open(fileobj=io.BytesIO(archive)) as source:
        source.extractall(directory, filter="data")
    recorder = Path(directory) / "dynamicrecorder"
    recorder.mkdir()
    for name in ("dynamic_corpus_test.go", "dynamic_deep_corpus_test.go", "dynamic_reconstructed_corpus_test.go"):
        text = (ROOT / "fieldmask/update" / name).read_text()
        if name == "dynamic_corpus_test.go":
            # Copy only fixture/recording helpers; no test function is executed.
            start = text.index("func TestRecordDynamicCorpus(")
            end = text.index("func dynamicCaseOptions(")
            text = text[:start] + text[end:]
            for package in ("bytes", "compress/gzip", "io", "os", "os/exec", "testing"):
                text = text.replace(f'\n\t"{package}"', "")
        (recorder / name.replace("_test", "")).write_text(text.replace("package update_test", "package main", 1))
    (recorder / "main.go").write_text(RECORDER)
    environment = dict(os.environ)
    # Neither a caller's workspace nor module flags may replace archived code.
    environment["GOWORK"] = "off"
    environment["GOFLAGS"] = ""
    environment.setdefault("GOCACHE", str(Path(directory) / "gocache"))
    print(f"recording git archive {BASE}", flush=True)
    subprocess.run(["go", "run", "./dynamicrecorder", str(OUTPUT)], cwd=directory, env=environment, check=True)
