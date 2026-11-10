// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"
)

// TestKeepIdentity pins that KeepIdentity leaves of a message the fields
// that address it under the update policy, as Apply keeps them under a mask
// that selects another field: the identity fields (id and etag by default,
// or the ones of WithIdentityFields) and the IDENTIFIER fields of the
// resolver, but not an identifier that is also OUTPUT_ONLY.
func TestKeepIdentity(t *testing.T) {
	t.Parallel()

	md := planDescriptor(t)
	sent := map[string]string{
		"id": "r-1", "etag": "e", "name": "n", "code": "c", "secret": "s", "create_time": "client",
		"filters.keywords": "k", "status.note": "note", "status.checked": "client", "status.inner.x": "x",
		"status.inner.server": "client", "channel": "ch",
	}
	resolve := func(name string, behaviors ...annotations.FieldBehavior) update.Option {
		return update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if string(fd.Name()) == name {
				return behaviors
			}

			return fieldbehavior.AnnotationResolver{}.Resolve(fd)
		}))
	}

	tests := []struct {
		name string
		opts []update.Option
		want map[string]string
	}{
		{name: "default identity", want: map[string]string{"id": "r-1", "etag": "e"}},
		{name: "identity fields", opts: []update.Option{update.WithIdentityFields("code")}, want: map[string]string{"code": "c"}},
		{
			name: "resolver identifier",
			opts: []update.Option{resolve("name", annotations.FieldBehavior_IDENTIFIER)},
			want: map[string]string{"id": "r-1", "etag": "e", "name": "n"},
		},
		{
			name: "output-only identifier",
			opts: []update.Option{resolve("name", annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_OUTPUT_ONLY)},
			want: map[string]string{"id": "r-1", "etag": "e"},
		},
		{
			name: "message identity field",
			opts: []update.Option{update.WithIdentityFields("status")},
			want: map[string]string{"status.note": "note", "status.inner.x": "x"},
		},
		{
			name: "message resolver identifier",
			opts: []update.Option{resolve("status", annotations.FieldBehavior_IDENTIFIER)},
			want: map[string]string{"id": "r-1", "etag": "e", "status.note": "note", "status.inner.x": "x"},
		},
		{
			name: "output-only identity field",
			opts: []update.Option{resolve("etag", annotations.FieldBehavior_OUTPUT_ONLY)},
			want: map[string]string{"id": "r-1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := planResource(md, sent)
			require.NoError(t, update.KeepIdentity(m, tc.opts...))
			require.True(t, proto.Equal(planResource(md, tc.want), m), "%v", m)

			// Apply keeps the same fields beside the one a mask selects.
			applied := planResource(md, sent)
			require.NoError(t, update.Apply(fieldmask.FromPaths("filters.keywords"), applied, tc.opts...))
			applied.Clear(md.Fields().ByName("filters"))
			require.True(t, proto.Equal(m, applied), "Apply keeps %v", applied)
		})
	}

	require.NoError(t, update.KeepIdentity(nil))

	empty := planResource(md, nil)
	require.NoError(t, update.KeepIdentity(empty))
	require.True(t, proto.Equal(planResource(md, nil), empty))
}
