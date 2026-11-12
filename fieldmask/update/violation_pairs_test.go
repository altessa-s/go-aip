// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"slices"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"

	"github.com/altessa-s/go-aip/internal/behavior"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func TestCheckSetProtectedConcretePairs(t *testing.T) {
	for _, b := range []annotations.FieldBehavior{annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_IMMUTABLE} {
		u := &updateCheck{policy: newOptions(), verify: true, validationOnly: true}
		pairs := []msgPair{
			{req: (&pb.Profile{Id: "wrong-a"}).ProtoReflect(), cur: (&pb.Profile{Id: "a"}).ProtoReflect(), path: "aliases.0"},
			{req: (&pb.Profile{Id: "same"}).ProtoReflect(), cur: (&pb.Profile{Id: "same"}).ProtoReflect(), path: "aliases.1"},
			{req: (&pb.Profile{Id: "wrong-b"}).ProtoReflect(), cur: (&pb.Profile{Id: "b"}).ProtoReflect(), path: "aliases.2"},
		}
		fd := pairs[0].req.Descriptor().Fields().ByName("id")
		u.checkSetProtected(fd, pairs, "aliases.*.id", b)

		want := []FieldViolation{behavior.ProtectedViolation("aliases.0.id", b), behavior.ProtectedViolation("aliases.2.id", b)}
		if !slices.Equal(want, u.violations) {
			t.Fatalf("%s: got %v, want %v", b, u.violations, want)
		}
	}
}
