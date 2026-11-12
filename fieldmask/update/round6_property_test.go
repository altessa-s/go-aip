// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"
	"github.com/altessa-s/go-aip/internal/maskmerge"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// Generate equal overlapping prefixes deliberately: random independent values
// rarely expose a count check lost when an apparently equal path is pruned.
func checkGeneratedOriginalSelections(t *testing.T, rng *rand.Rand, count int) {
	t.Helper()

	md := messageChoiceDescriptor(t)
	plain := plainMessageChoiceDescriptor(t)

	for i := range count {
		b := annotations.FieldBehavior_IMMUTABLE
		if rng.IntN(2) == 0 {
			b = annotations.FieldBehavior_IDENTIFIER
		}

		var req, stored proto.Message

		var paths, protected []string

		var oraclePaths []string

		if i%8 == 5 {
			req = parseMessageChoice(t, plain, `config { a {} }`)
			stored = parseMessageChoice(t, plain, `config { b: "stored" }`)
			protected = []string{"config.b"}
		} else if i%8 == 0 {
			req = parseMessageChoice(t, md, `config { a {} child { note: "new" } }`)
			stored = parseMessageChoice(t, md, `config { b: "stored" child { note: "old" } }`)
			paths = []string{"config.a.note"}
			// The base reports the directly selected change before the oneof
			// clear newly detected by the merge-effect check.
			protected = []string{"config.child.note", "config.b", "config.a.note"}

			if rng.IntN(2) == 0 {
				paths = append(paths, "config.child.note")
			}
		} else {
			r, c := &pb.Account{}, &pb.Account{}
			field := "members"
			n := 1 + rng.IntN(3)

			if i%8 < 3 || i%8 == 6 {
				for range n {
					c.Members = append(c.Members, &pb.Owner{Email: "e", Note: "same"})
				}

				r = proto.CloneOf(c)
				r.Members = append(r.Members, &pb.Owner{Email: "e", Note: "extra"})
			} else {
				field = "delegates"
				c.Delegates = map[string]*pb.Owner{}

				for k := range n {
					c.Delegates[fmt.Sprintf("k%d", k)] = &pb.Owner{Email: "e", Note: "same"}
				}

				r = proto.CloneOf(c)
				r.Delegates["extra"] = &pb.Owner{Email: "e", Note: "extra"}
			}

			if rng.IntN(2) == 0 {
				r, c = c, r
			}

			path := field + ".*.note"
			if i%8 == 2 || i%8 == 4 {
				path = field // whole replacements include removed elements
			}

			if i%8 >= 6 {
				path = field + ".*.create_time"
			}

			paths, protected = []string{path}, []string{field}

			if rng.IntN(2) == 0 {
				r, c = &pb.Account{Parent: r}, &pb.Account{Parent: c}
				paths, protected = []string{"parent." + path}, []string{"parent"}
			}

			req, stored = r, c
		}

		if i%8 < 5 {
			oraclePaths = paths
		}

		t.Run(fmt.Sprintf("original=%04d/%s", i, b), func(t *testing.T) {
			resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
				for _, path := range protected {
					parts := strings.Split(path, ".")
					if string(fd.Name()) == parts[len(parts)-1] {
						return []annotations.FieldBehavior{b}
					}
				}

				return fieldbehavior.AnnotationResolver{}.Resolve(fd)
			})
			opts := []update.Option{update.WithResolver(resolver)}
			after := proto.Clone(stored)
			mergeErr := update.MergePaths(after, req, oraclePaths, opts...)
			changes, err := maskmerge.ProtectedChanges(req.ProtoReflect(), stored.ProtoReflect(), fieldmask.FromPaths(paths...), resolver.Resolve, "", nil)

			if mergeErr != nil {
				if !errors.Is(err, update.ErrElementCountMismatch) || len(changes) != 0 {
					t.Fatalf("failed merge must not produce equality: merge=%v changes=%v err=%v", mergeErr, changes, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}

			var changed []string

			for _, path := range protected {
				if !proto.Equal(protectedPathValue(stored, path), protectedPathValue(after, path)) {
					changed = append(changed, path)
				}
			}

			if mergeErr == nil && len(changes) != len(changed) {
				t.Fatalf("comparator differs from public merge: got=%v want=%v", changes, changed)
			}

			for _, entry := range round6Entries {
				t.Run(entry, func(t *testing.T) {
					mask, err := round2Entry(t, entry, proto.Clone(req), stored, maskOf(paths), opts...)
					if err == nil {
						update.AssertWritableSelection(t, maskOf(paths), req, mask, opts...)
					}

					if mergeErr != nil {
						if !errors.Is(err, update.ErrElementCountMismatch) {
							t.Fatalf("original merge error identity lost: merge=%v update=%v mask=%v", mergeErr, err, paths)
						}

						return
					}

					want := changed

					switch entry {
					case "Spec", "DeferredSpec", "Patch", "DeferredPatch":
						// These APIs preserve the base's single located refusal.
						// The aggregate APIs below must still name every change.
						if len(want) > 0 {
							if _, ok := errors.AsType[*update.MaskPathError](err); !ok {
								t.Fatalf("want a located plan refusal, got %v", err)
							}

							want = want[:1]
						}
					}

					for _, path := range want {
						requireProtectedViolation(t, err, path, b)
					}

					if len(changed) == 0 && err != nil {
						t.Fatalf("unchanged protected values rejected: %v mask=%v", err, paths)
					}
				})
			}
		})
	}
}

func protectedPathValue(resource proto.Message, path string) proto.Message {
	m := resource.ProtoReflect()
	parts := strings.Split(path, ".")

	for _, name := range parts[:len(parts)-1] {
		fd := m.Descriptor().Fields().ByName(protoreflect.Name(name))
		m = m.Get(fd).Message()
	}

	return protectedInputValue(m.Interface(), parts[len(parts)-1])
}
