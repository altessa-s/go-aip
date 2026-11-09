// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSliceOptionsSnapshotAndEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		option func([]string) any
		get    func(*policyOptions, *translatorOptions) []string
	}{
		{"denied", func(v []string) any { return WithDeniedFields(v...) }, func(p *policyOptions, _ *translatorOptions) []string { return p.deniedFields }},
		{"denied storage", func(v []string) any { return WithDeniedStorageFields(v...) }, func(_ *policyOptions, o *translatorOptions) []string { return o.deniedStorageFields }},
		{"required", func(v []string) any { return WithRequiredFields(v...) }, func(_ *policyOptions, o *translatorOptions) []string { return o.requiredFields }},
		{"view", func(v []string) any { return WithView(ViewBasic, v...) }, func(p *policyOptions, _ *translatorOptions) []string { return p.views[ViewBasic] }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			input := []string{" one ", "one"}
			option := tc.option(input)
			input[0] = " before apply "
			p, o := apply(option)
			input[0] = "after apply"

			require.Equal(t, []string{" before apply ", "one"}, tc.get(p, o))
			p, o = apply(option, tc.option(nil))
			require.Nil(t, tc.get(p, o))
		})
	}
}

// apply applies each option to the options struct of its layer.
func apply(opts ...any) (*policyOptions, *translatorOptions) {
	p, o := newPolicyOptions(), newTranslatorOptions()

	for _, opt := range opts {
		switch opt := opt.(type) {
		case PolicyOption:
			opt(p)
		case TranslatorOption:
			opt(o)
		}
	}

	return p, o
}

func TestMappingOptionsSnapshotAndEmpty(t *testing.T) {
	t.Parallel()

	allowed := []string{"name"}
	exact := map[string]string{"createdAt": "created_at", " spaced ": " storage "}
	prefix := map[string]string{"address.": "addr.", " area.": " space."}
	allowedOption := WithAllowedFields(allowed...)
	exactOption := WithFieldMapping(exact)
	prefixOption := WithFieldPrefixMapping(prefix)
	allowed[0] = "mutated"
	exact["createdAt"] = "mutated"
	prefix["address."] = "mutated."
	p := newPolicyOptions(allowedOption)
	o := newTranslatorOptions(exactOption, prefixOption)

	require.True(t, p.allowedFields.Allows("mutated"))
	require.Equal(t, "mutated", o.fieldMapping.Apply("createdAt"))
	require.Equal(t, " storage ", o.fieldMapping.Apply(" spaced "))
	require.Equal(t, "mutated.city", o.fieldMapping.Apply("address.city"))
	require.Equal(t, " space.city", o.fieldMapping.Apply(" area.city"))

	WithAllowedFields()(p)
	WithFieldMapping(nil)(o)
	WithFieldPrefixMapping(nil)(o)
	require.True(t, p.allowedFields.IsEmpty())
	require.Equal(t, "createdAt", o.fieldMapping.Apply("createdAt"))
	require.Equal(t, "address.city", o.fieldMapping.Apply("address.city"))
}

func TestAllowedFieldsWhitespaceAndEmpty(t *testing.T) {
	t.Parallel()

	p := newPolicyOptions(WithAllowedFields(" ", "name", "name"))
	require.True(t, p.allowedFields.Allows(" "))
	require.True(t, p.allowedFields.Allows("name"))
	require.False(t, p.allowedFields.Allows("other"))

	empty := newPolicyOptions(WithAllowedFields())
	require.True(t, empty.allowedFields.IsEmpty())
}

func TestBooleanOptionsCanBeDisabled(t *testing.T) {
	t.Parallel()

	o := newParserOptions(WithNoCache(true), WithNoCache(false))
	require.False(t, o.noCache)

	p := newPolicyOptions(WithUntrustedInput(true), WithUntrustedInput(false))
	require.False(t, p.untrustedInput)
}
