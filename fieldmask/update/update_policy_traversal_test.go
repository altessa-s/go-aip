// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"
)

func TestUpdateSynthesisAndExpansionPolicy(t *testing.T) {
	t.Parallel()
	m := dynamicpb.NewMessage(planDescriptor(t))
	fd := m.Descriptor().Fields().ByName("filters")
	sub := m.Mutable(fd).Message()
	sub.Set(sub.Descriptor().Fields().ByName("keywords"), protoreflect.ValueOfString("kept"))

	resolver := update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if fd.Name() == "keywords" {
			return []annotations.FieldBehavior{annotations.FieldBehavior_IDENTIFIER}
		}

		return nil
	}))
	require.NotContains(t, update.FromSetFields(m, resolver).ToProtoFieldMask().Paths, "filters.keywords")
	expanded := update.ExpandMessagePaths((fieldmask.FieldMask{"filters": nil}), m, resolver)
	require.NotContains(t, expanded["filters"], "keywords")

	atomic := update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool { return md.Name() == "Filters" })
	require.Equal(t, fieldmask.FieldMask{"filters": nil}, update.ExpandMessagePaths((fieldmask.FieldMask{"filters": nil}), m, atomic))
	require.Contains(t, update.FromSetFields(m, resolver, atomic), "filters")
}
