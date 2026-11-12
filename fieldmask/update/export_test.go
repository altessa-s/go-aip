// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/maskpath"
)

// SynthesisSkipFields exposes synthesisSkipFields to the black-box tests.
func SynthesisSkipFields(msg protoreflect.Message) []string {
	return synthesisSkipFields(msg, newOptions())
}

// SelectsAll exposes selectsAll, the full-replacement check of raw update
// mask paths, to the black-box tests.
func SelectsAll(paths []string) (bool, error) {
	return selectsAll(paths)
}

// AssertWritableSelection checks that every returned write is covered by the
// selection passed to merge-effect evaluation, including keyed exclusions.
// It exists only in tests; no assertion hook is installed in production code.
func AssertWritableSelection(t testing.TB, raw *fieldmaskpb.FieldMask, request proto.Message, writable fieldmask.FieldMask, opts ...Option) {
	t.Helper()

	u, root, err := newApplyCheck(request, nil, newOptions(opts...))
	if err != nil {
		t.Fatal(err)
	}

	selection := fieldmask.FromPaths(raw.GetPaths()...)
	if raw == nil {
		selection = u.impliedSelection(root)
	} else if all, err := selectsAll(raw.Paths); err != nil {
		t.Fatal(err)
	} else if all {
		selection = u.replacementSelection(root.req.Descriptor())
	}

	u.inputSelection(selection, root.req.Descriptor())

	if u.refusal != nil {
		t.Fatal(u.refusal)
	}

	for _, path := range writable.ToPaths() {
		if !selectionCovers(selection, maskpath.Split(path)) {
			t.Fatalf("writable path %q is outside evaluated selection %v (raw=%v)", path, selection, raw)
		}
	}
}

func selectionCovers(selection fieldmask.FieldMask, path []string) bool {
	if selection == nil {
		return true // whole-field replacement covers all descendants
	}

	if len(path) == 0 {
		return false // a descendant selection cannot cover a whole replacement
	}

	nested, ok := selection[path[0]]
	if !ok {
		nested, ok = selection[maskpath.Wildcard]
	}

	return ok && selectionCovers(nested, path[1:])
}
