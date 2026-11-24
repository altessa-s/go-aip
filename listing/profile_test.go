// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing_test

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/orderby"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func policySchema(t *testing.T, opts ...listing.ProfileOption) *listing.Schema {
	t.Helper()

	s, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "name", Kind: filter.FieldKindString},
		{Name: "at", Kind: filter.FieldKindTimestamp},
	}, []string{"id"}, listing.NewProfile(opts...))
	require.NoError(t, err)

	return s
}

func TestProfileFilterPolicies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, expr string
		opts       []listing.ProfileOption
	}{
		{"portable", `name in []`, []listing.ProfileOption{listing.WithFilterOptions(filter.WithPortableContract(false))}},
		{"individual contract rule", `name == id`, []listing.ProfileOption{listing.WithFilterOptions(filter.WithContractRules(map[filter.ContractRule]bool{filter.LiteralComparisons: false}))}},
		{"safe regex", `name.matches("a+")`, []listing.ProfileOption{listing.WithBuiltinFunctions("matches"), listing.WithFilterOptions(filter.WithPortableContract(false))}},
		{"trusted regex", `name.matches("(a|b)+")`, []listing.ProfileOption{listing.WithBuiltinFunctions("matches"), listing.WithFilterOptions(filter.WithPortableContract(false), filter.WithUntrustedInput(false), filter.WithSafeRegex(false), filter.WithUnsafeRegex(true))}},
		{"size disabled", `size(name) == -1`, []listing.ProfileOption{listing.WithSizeValidator(nil)}},
		{"presence disabled", `name == null`, []listing.ProfileOption{listing.WithPresenceValidator(nil)}},
		{"call disabled", `name.contains("x")`, []listing.ProfileOption{listing.WithCallValidator(func(*filter.CallNode) error { return errors.New("disabled") }), listing.WithCallValidator(nil)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := policySchema(t, tt.opts...)
			q, err := s.Compile(t.Context(), listing.Request{Filter: tt.expr})
			require.NoError(t, err)
			p, err := filter.NewPolicy(s.FilterOptions()...)
			require.NoError(t, err)
			require.NoError(t, filter.Validate(q.Filter, p))
		})
	}
}

// TestProfileSubstringRejected keeps substring() out of every listing request, even with the builtin allowed and the portable
// rules off: the request validation takes a field or a literal in operand position, so no engine receives it.
func TestProfileSubstringRejected(t *testing.T) {
	t.Parallel()

	s := policySchema(t, listing.WithBuiltinFunctions("substring"), listing.WithFilterOptions(filter.WithPortableContract(false),
		filter.WithContractRules(map[filter.ContractRule]bool{filter.LiteralComparisons: false})))

	for _, expr := range []string{`name.substring(1) == "a"`, `"a" == name.substring(1)`, `name.substring(0, 1) in ["a"]`, `name == name.substring(1)`} {
		_, err := s.Compile(t.Context(), listing.Request{Filter: expr})
		require.True(t, errors.Is(err, filter.ErrUnsupportedOperation) || errors.Is(err, filter.ErrInvalidExpression), "%s: %v", expr, err)
	}
}

func TestProfileReplacementChecks(t *testing.T) {
	t.Parallel()

	cause := errors.New("application policy")

	check := func(filter.Node, *listing.Schema) error { return cause }
	for _, option := range []listing.ProfileOption{
		listing.WithSizeValidator(check), listing.WithPresenceValidator(check),
		listing.WithCallValidator(func(*filter.CallNode) error { return cause }),
	} {
		t.Run("replacement", func(t *testing.T) {
			t.Parallel()
			_, err := policySchema(t, option).Compile(t.Context(), listing.Request{Filter: `name.contains("x")`})
			require.ErrorIs(t, err, cause)
		})
	}
}

func TestProfileBuiltinAllowlist(t *testing.T) {
	t.Parallel()
	_, err := policySchema(t, listing.WithBuiltinFunctions()).Compile(t.Context(), listing.Request{Filter: `name.contains("x")`})
	require.ErrorIs(t, err, filter.ErrFunctionNotAllowed)
	_, err = policySchema(t, listing.WithBuiltinFunctions("endsWith")).Compile(t.Context(), listing.Request{Filter: `name.endsWith("x")`})
	require.NoError(t, err)
}

func TestProfileTimestampPrecision(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)

	for _, precision := range []time.Duration{0, time.Microsecond, time.Second} {
		t.Run(precision.String(), func(t *testing.T) {
			t.Parallel()

			s := policySchema(t, listing.WithTimestampPrecision(precision), listing.WithDefaultEqualities(map[string]any{"at": at}))
			for _, expr := range []string{"", `at == timestamp("2026-01-02T03:04:05.123456789Z")`} {
				q, err := s.Compile(t.Context(), listing.Request{Filter: expr})
				require.NoError(t, err)

				lit := q.Filter.(*filter.BinaryOpNode).Right.(*filter.LiteralNode)
				require.Equal(t, at.Truncate(precision), lit.Value)
			}
		})
	}
}

func TestProfileSortPolicies(t *testing.T) {
	t.Parallel()
	s := policySchema(t, listing.WithUnrestrictedOrder(true), listing.WithDefaultOrderBy("name desc"))
	require.Equal(t, "name desc", s.Default().String())
	order, err := s.Order(t.Context(), "at, name desc")
	require.NoError(t, err)
	require.Len(t, order.Keys(), 2)
	_, err = s.Order(t.Context(), "unknown")
	require.Error(t, err)

	fields := []listing.Field{{Name: "id", Kind: filter.FieldKindString, Optional: true}}
	_, err = listing.NewSchema(fields, []string{"id"}, listing.NewProfile(listing.WithSortFieldValidator(nil)))
	require.NoError(t, err)

	cause := errors.New("sort denied")
	_, err = listing.NewSchema(fields, []string{"id"}, listing.NewProfile(listing.WithSortFieldValidator(func(f listing.Field) error { require.Equal(t, "id", f.Name); return cause })))
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
	require.ErrorIs(t, err, cause)
}

func TestProfileDefaultLiteral(t *testing.T) {
	t.Parallel()
	s := policySchema(t, listing.WithDefaultEqualities(map[string]any{"name": 42}), listing.WithDefaultLiteral(func(f listing.Field, v any) (any, error) {
		require.Equal(t, "name", f.Name)
		require.Equal(t, 42, v)

		return "converted", nil
	}))
	q, err := s.Compile(t.Context(), listing.Request{})
	require.NoError(t, err)
	require.Equal(t, "converted", q.Filter.(*filter.BinaryOpNode).Right.(*filter.LiteralNode).Value)
}

func TestProfileRequestErrorsAndPageSize(t *testing.T) {
	t.Parallel()

	classified := errors.New("application invalid request")
	formatter := func(field, reason string, cause error) error {
		return &coreerrs.FieldError{Field: "api_" + field, Reason: reason, Err: errors.Join(classified, cause)}
	}

	s := policySchema(t, listing.WithErrorFormatter(formatter), listing.WithParentRequired(true))
	for _, req := range []listing.Request{{PageSize: -1}, {}, {Parent: "p", OrderBy: "name"}, {Parent: "p", Filter: "("}, {Parent: "p", Filter: `size(name) == -1`}, {Parent: "p", Filter: `name == null`}, {Parent: "p", ReadMask: []string{"name"}}} {
		_, err := s.Compile(t.Context(), req)
		require.ErrorIs(t, err, classified)
	}

	require.ErrorIs(t, s.TranslationError(filter.ErrInvalidExpression), classified)
	s = policySchema(t, listing.WithPageSizeResolver(func(n int32, def, limit int) (int, error) { require.Equal(t, int32(-1), n); return 17, nil }))
	q, err := s.Compile(t.Context(), listing.Request{PageSize: -1})
	require.NoError(t, err)
	require.Equal(t, 17, q.Size)
}

func TestProfileParserOptions(t *testing.T) {
	t.Parallel()
	s := policySchema(t, listing.WithFilterParserOptions(filterparser.WithNoCache(true)), listing.WithOrderParserOptions(orderby.WithNoCache(true)))
	_, err := s.Compile(t.Context(), listing.Request{Filter: `name == "x"`})
	require.NoError(t, err)
}

func TestProfileRegexPolicyReplacement(t *testing.T) {
	t.Parallel()

	cause := errors.New("regex policy")
	s := policySchema(t, listing.WithBuiltinFunctions("matches"), listing.WithFilterOptions(
		filter.WithPortableContract(false), filter.WithRegexValidator(func(string) error { return cause }),
	))
	_, err := s.Compile(t.Context(), listing.Request{Filter: `name.matches("x")`})
	require.ErrorIs(t, err, cause)
	s = policySchema(t, listing.WithBuiltinFunctions("matches"), listing.WithFilterOptions(filter.WithSafeRegex(false)))
	_, err = s.Compile(t.Context(), listing.Request{Filter: `name.matches("x")`})
	require.ErrorIs(t, err, filter.ErrFunctionNotAllowed)
}

func TestProfileCapturesInputs(t *testing.T) {
	t.Parallel()

	defaults := map[string]any{"name": "original"}
	names := []string{"contains"}
	profile := listing.NewProfile(listing.WithDefaultEqualities(defaults), listing.WithBuiltinFunctions(names...))
	defaults["name"] = "changed"
	names[0] = "matches"

	for range 2 {
		s, err := listing.NewSchema([]listing.Field{{Name: "name", Kind: filter.FieldKindString}}, []string{"name"}, profile)
		require.NoError(t, err)
		q, err := s.Compile(t.Context(), listing.Request{})
		require.NoError(t, err)
		require.Equal(t, "original", q.Filter.(*filter.BinaryOpNode).Right.(*filter.LiteralNode).Value)
		_, err = s.Compile(t.Context(), listing.Request{Filter: `name.contains("x")`})
		require.NoError(t, err)
	}
}

func TestProfileSortEligibilityReplacement(t *testing.T) {
	t.Parallel()

	for _, field := range []listing.Field{
		{Name: "x", Kind: filter.FieldKindString, Array: true},
		{Name: "x", Kind: filter.FieldKindString, Presence: true},
		{Name: "x", Kind: filter.FieldKindBytes},
	} {
		t.Run(field.Kind.String(), func(t *testing.T) {
			t.Parallel()

			_, err := listing.NewSchema([]listing.Field{field}, []string{"x"}, nil)
			require.ErrorIs(t, err, listing.ErrInvalidSchema)
			_, err = listing.NewSchema([]listing.Field{field}, []string{"x"}, listing.NewProfile(listing.WithSortFieldValidator(nil)))
			require.NoError(t, err)
		})
	}
}

func TestProfileRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	for _, option := range []listing.ProfileOption{listing.WithDefaultLiteral(nil), listing.WithErrorFormatter(nil), listing.WithTimestampPrecision(-1)} {
		_, err := listing.NewSchema([]listing.Field{{Name: "x", Kind: filter.FieldKindString}}, []string{"x"}, listing.NewProfile(option))
		require.ErrorIs(t, err, listing.ErrInvalidSchema)
	}
}

func TestParserOptionsPreserveSchemaVocabulary(t *testing.T) {
	t.Parallel()

	s, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "state", Kind: filter.FieldKindString, Enum: map[string]any{"ACTIVE": "active"}},
	}, []string{"id"}, listing.NewProfile(listing.WithFilterParserOptions(filterparser.WithEnumNames(map[string]map[string]any{}))))
	require.NoError(t, err)
	q, err := s.Compile(t.Context(), listing.Request{Filter: `state == "ACTIVE"`})
	require.NoError(t, err)
	require.Equal(t, "active", q.Filter.(*filter.BinaryOpNode).Right.(*filter.LiteralNode).Value)
}

func TestPageSizeResolverInvalidResult(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, -1, int(^uint(0) >> 1)} {
		s := policySchema(t, listing.WithPageSizeResolver(func(int32, int, int) (int, error) { return size, nil }))
		_, err := s.Compile(t.Context(), listing.Request{})
		require.Error(t, err)
	}
}

func TestDefaultEqualitiesDecimalAndBytes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		kind  filter.FieldKind
		value any
	}{
		{filter.FieldKindDecimal, decimal.RequireFromString("1.00000000000000001")},
		{filter.FieldKindBytes, []byte("abc")},
	} {
		schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "v", Kind: tc.kind}}, []string{"id"}, listing.NewProfile(listing.WithDefaultEqualities(map[string]any{"v": tc.value})))
		require.NoError(t, err)
		_, err = schema.Compile(t.Context(), listing.Request{})
		require.NoError(t, err)
	}
}

func TestContainsFieldContract(t *testing.T) {
	t.Parallel()

	names := []string{"name", "id", "name"}
	opt := listing.WithCaseInsensitiveContainsFields(names...)
	names[0] = "missing"
	schema := policySchema(t, opt)
	got := schema.CaseInsensitiveContainsFields()
	require.Equal(t, []string{"id", "name"}, got)
	got[0] = "missing"

	require.Equal(t, []string{"id", "name"}, schema.CaseInsensitiveContainsFields())
	require.Equal(t, schema.ContractID(), policySchema(t, listing.WithCaseInsensitiveContainsFields("id", "name")).ContractID())
	require.NotEqual(t, schema.ContractID(), policySchema(t, listing.WithCaseInsensitiveContainsFields("name")).ContractID())
	require.NotEqual(t, schema.ContractID(), policySchema(t, opt, listing.WithContractID("v2")).ContractID())
	require.Equal(t, listing.DefaultContractID, policySchema(t, listing.WithCaseInsensitiveContainsFields()).ContractID())

	fields := []listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "kind", Kind: filter.FieldKindString, Enum: map[string]any{"FOO": "foo"}},
		{Name: "num", Kind: filter.FieldKindInt},
	}

	for _, name := range []string{"unknown", " id", "kind", "num"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := listing.NewSchema(fields, []string{"id"}, listing.NewProfile(listing.WithCaseInsensitiveContainsFields(name)))
			require.ErrorIs(t, err, listing.ErrInvalidSchema)
		})
	}

	_, err := listing.NewSchema(fields, []string{"id"}, listing.NewProfile(listing.WithContractID(schema.ContractID())))
	require.ErrorIs(t, err, listing.ErrInvalidSchema)

	restricted := policySchema(t, opt, listing.WithFilterOptions(filter.WithAllowedFields("id")))
	_, err = restricted.Compile(t.Context(), listing.Request{Filter: `name.contains("x")`})
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
}
