// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package resourcename_test

import (
	"testing"

	"github.com/altessa-s/go-aip/resourcename"

	rntv1 "github.com/altessa-s/go-aip/internal/proto/gen/resourcenametest/v1"
)

const benchName = "projects/my-project/locations/us-east1/keyRings/ring-1/cryptoKeys/key-42"

var benchPattern = resourcename.MustParsePattern("projects/{project}/locations/{location}/keyRings/{key_ring}/cryptoKeys/{crypto_key}")

func BenchmarkPatternMatches(b *testing.B) {
	b.ReportAllocs()

	for b.Loop() {
		if !benchPattern.Matches(benchName) {
			b.Fatal("no match")
		}
	}
}

func BenchmarkPatternAppendValues(b *testing.B) {
	b.ReportAllocs()

	dst := make([]string, 0, 8)

	for b.Loop() {
		if _, err := benchPattern.AppendValues(dst[:0], benchName); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPatternMatchMap(b *testing.B) {
	b.ReportAllocs()

	for b.Loop() {
		if _, err := benchPattern.Match(benchName); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPatternFormat(b *testing.B) {
	b.ReportAllocs()

	for b.Loop() {
		if _, err := benchPattern.FormatValues("my-project", "us-east1", "ring-1", "key-42"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResourceMatchMultiPattern(b *testing.B) {
	r, err := resourcename.NewResource("library.example.com/Shelf",
		"projects/{project}/shelves/{shelf}",
		"folders/{folder}/shelves/{shelf}",
		"organizations/{organization}/shelves/{shelf}",
		"billingAccounts/{billing_account}/shelves/{shelf}",
	)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for b.Loop() {
		if !r.Matches("billingAccounts/b1/shelves/s1") {
			b.Fatal("no match")
		}
	}
}

func BenchmarkValidatorCheckNameStrict(b *testing.B) {
	v := resourcename.NewValidator(resourcename.WithIDValidator(resourcename.StrictID))

	r, err := resourcename.NewResource("cloudkms.example.com/CryptoKey", benchPattern.String())
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for b.Loop() {
		if err := v.CheckName("name", benchName, r); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValidateRequest(b *testing.B) {
	v := resourcename.NewValidator()
	if err := v.RegisterFile(rntv1.File_resourcenametest_v1_test_proto); err != nil {
		b.Fatal(err)
	}

	req := &rntv1.BatchGetBooksRequest{Parent: "publishers/p", Names: []string{"publishers/p/books/a", "publishers/p/books/b"}}

	b.ReportAllocs()

	for b.Loop() {
		if err := v.ValidateRequest(req); err != nil {
			b.Fatal(err)
		}
	}
}
