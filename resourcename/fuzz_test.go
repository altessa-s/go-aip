// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package resourcename_test

import (
	"errors"
	"testing"

	"github.com/altessa-s/go-aip/resourcename"
)

// FuzzPatternMatchFormat checks that a matched name formats back to itself
// and that a mismatch is always explained by a *NameError.
func FuzzPatternMatchFormat(f *testing.F) {
	for _, s := range []string{"publishers/a/books/b", "publishers//books/b", "publishers/a/books", "/", "", "publishers/-/books/x/y"} {
		f.Add(s)
	}

	p := resourcename.MustParsePattern("publishers/{publisher}/books/{book}")

	f.Fuzz(func(t *testing.T, name string) {
		vals, err := p.Match(name)
		if err != nil {
			if _, ok := errors.AsType[*resourcename.NameError](err); !ok {
				t.Fatalf("mismatch of %q is not a *NameError: %v", name, err)
			}

			if p.Matches(name) {
				t.Fatalf("Matches(%q) disagrees with Match", name)
			}

			return
		}

		got, err := p.Format(vals)
		if err != nil {
			t.Fatalf("Format(%v): %v", vals, err)
		}

		if got != name {
			t.Fatalf("round trip %q -> %q", name, got)
		}
	})
}

// FuzzParsePattern checks that a parsed pattern matches the name made from
// its own variables.
func FuzzParsePattern(f *testing.F) {
	for _, s := range []string{"publishers/{publisher}", "users/{user}/config", "a/{bb}/c", "{x}", "a//b"} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, pattern string) {
		p, err := resourcename.ParsePattern(pattern)
		if err != nil {
			return
		}

		values := make([]string, len(p.Variables()))
		for i := range values {
			values[i] = "id"
		}

		name, err := p.FormatValues(values...)
		if err != nil {
			t.Fatalf("FormatValues on %q: %v", pattern, err)
		}

		if !p.Matches(name) {
			t.Fatalf("%q does not match its own name %q", pattern, name)
		}
	})
}
