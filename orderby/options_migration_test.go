// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package orderby

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMappingOptionsSnapshotAndEmpty(t *testing.T) {
	t.Parallel()

	exact := map[string]string{"createdAt": "created_at", " spaced ": " storage "}
	prefixes := map[string]string{"address.": "addr.", " area.": " space."}
	o := newTranslatorOptions([]TranslatorOption{WithFieldMapping(exact), WithFieldPrefixMapping(prefixes)}...)
	exact["createdAt"] = "mutated"
	prefixes["address."] = "mutated."

	require.Equal(t, "created_at", o.ApplyFieldMapping("createdAt"))
	require.Equal(t, " storage ", o.ApplyFieldMapping(" spaced "))
	require.Equal(t, "addr.city", o.ApplyFieldMapping("address.city"))
	require.Equal(t, " space.city", o.ApplyFieldMapping(" area.city"))

	WithFieldMapping(nil)(o)
	WithFieldPrefixMapping(nil)(o)
	require.Equal(t, "createdAt", o.ApplyFieldMapping("createdAt"))
	require.Equal(t, "address.city", o.ApplyFieldMapping("address.city"))
}

func TestAllowedFieldsWhitespaceAndEmpty(t *testing.T) {
	t.Parallel()

	p := &Policy{policyOptions: *newPolicyOptions(WithAllowedFields(" ", "name", "name"))}
	require.True(t, p.IsFieldAllowed(" "))
	require.True(t, p.IsFieldAllowed("name"))
	require.False(t, p.IsFieldAllowed("other"))

	empty := &Policy{policyOptions: *newPolicyOptions(WithAllowedFields())}
	require.False(t, empty.IsFieldAllowed("anything"))
}

func TestBooleanOptionsCanBeDisabled(t *testing.T) {
	t.Parallel()

	o := newParserOptions(WithNoCache(true), WithNoCache(false))
	require.False(t, o.noCache)

	po := newPolicyOptions(WithUntrustedInput(true), WithUntrustedInput(false))
	require.False(t, po.untrustedInput)

	o = newParserOptions(WithAllowArrayIndexPaths(true), WithAllowArrayIndexPaths(false),
		WithCaseSensitiveDirection(true), WithCaseSensitiveDirection(false))
	require.False(t, o.allowArrayIndices)
	require.False(t, o.caseSensitiveDirection)
}
