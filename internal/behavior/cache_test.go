// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package behavior_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/behavior"

	testpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// TestShared_CachedAndIsolated pins the cached reader: repeated reads agree,
// an owned copy from Get never reaches the cache, and the shared slice has no
// spare capacity an append could write into.
func TestShared_CachedAndIsolated(t *testing.T) {
	t.Parallel()

	fields := (&testpb.Resource{}).ProtoReflect().Descriptor().Fields()

	tests := []struct {
		name  string
		field protoreflect.Name
		want  []annotations.FieldBehavior
	}{
		{"single", "id", []annotations.FieldBehavior{annotations.FieldBehavior_IDENTIFIER}},
		{"none", "description", nil},
		{"several", "slug", []annotations.FieldBehavior{annotations.FieldBehavior_REQUIRED, annotations.FieldBehavior_IMMUTABLE}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fd := fields.ByName(tt.field)
			require.NotNil(t, fd)

			require.Equal(t, tt.want, behavior.Shared(fd))
			require.Equal(t, tt.want, behavior.Shared(fd))

			owned := behavior.Get(fd)
			for i := range owned {
				owned[i] = annotations.FieldBehavior_INPUT_ONLY
			}

			shared := behavior.Shared(fd)
			require.Equal(t, tt.want, shared)
			require.Equal(t, len(shared), cap(shared))
		})
	}

	require.Nil(t, behavior.Shared(nil))
}

// TestSubtreeHasAny_CacheKeyedBySet pins that cached results are keyed by the
// behavior set, not only by the message type, and that concurrent readers
// agree.
func TestSubtreeHasAny_CacheKeyedBySet(t *testing.T) {
	t.Parallel()

	md := (&testpb.NestedGetResourceRequest{}).ProtoReflect().Descriptor()

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				require.True(t, behavior.SubtreeHasAny(md, annotations.FieldBehavior_REQUIRED))
				require.False(t, behavior.SubtreeHasAny(md, annotations.FieldBehavior_OUTPUT_ONLY))
				require.True(t, behavior.SubtreeHasAny(md, annotations.FieldBehavior_OUTPUT_ONLY, annotations.FieldBehavior_REQUIRED))
				require.False(t, behavior.SubtreeHasAny(md, 64, -1))
				require.False(t, behavior.SubtreeHasAny(nil, annotations.FieldBehavior_REQUIRED))
			}
		})
	}

	wg.Wait()
}
