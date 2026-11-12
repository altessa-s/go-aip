// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/filter/parser"

	filterfuncs "github.com/altessa-s/go-aip/filter/functions"
)

type loweredPredicate struct{ filter.Node }

func (n loweredPredicate) Lower() (filter.Node, error) { return n.Node, nil }

// TestExtensionPipeline: a custom function lowering to a core predicate translates (filter TestExtensionPipeline).
func TestExtensionPipeline(t *testing.T) {
	t.Parallel()

	p, err := parser.New(parser.WithCustomFunctions(map[string]filter.CustomFunction{
		"active": func([]filter.Node) (filter.Node, error) {
			return loweredPredicate{Node: &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "state"}, Right: &filter.LiteralNode{Value: "active"}}}, nil
		},
	}))
	require.NoError(t, err)

	n, err := p.Parse(t.Context(), "active()")
	require.NoError(t, err)

	tr, err := mongo.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("state")))
	require.NoError(t, err)

	_, err = tr.Translate(n)
	require.NoError(t, err)
}

// TestMongoSchemaIsBackendSpecific: a MongoDB schema configures the translator and the evaluator alike (filter
// TestMongoSchemaIsBackendSpecific).
func TestMongoSchemaIsBackendSpecific(t *testing.T) {
	t.Parallel()

	schema, err := mongo.NewSchema(
		mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"name": filter.FieldKindString}),
		mongo.WithSchemaOmitEmpty("name"),
		mongo.WithSchemaSizeZeroWhenAbsent(true),
	)
	require.NoError(t, err)

	_, err = mongo.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAllowedFields("name")), schema)
	require.NoError(t, err)
	_, err = evaluator.New(filtertest.MustPolicy(t, filter.WithAllowedFields("name")), evaluator.WithProfile(schema))
	require.NoError(t, err)
}

// TestConcurrentCompatibility: ValidateAST is safe for concurrent use (filter TestConcurrentCompatibility).
func TestConcurrentCompatibility(t *testing.T) {
	t.Parallel()

	mo, err := mongo.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)

	var tr filter.Compatibility = mo

	n := &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "name"}, Right: &filter.LiteralNode{Value: "hello"}}

	for range 12 {
		t.Run("shared", func(t *testing.T) {
			t.Parallel()

			for range 40 {
				require.NoError(t, tr.ValidateAST(n))
			}
		})
	}
}

// TestTranslatorBudgets: the operation and depth budgets apply to the translation (filter TestTranslatorBudgets).
func TestTranslatorBudgets(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, expr string
		options    []filter.PolicyOption
		want       error
	}{
		{"operations", `x == 1`, []filter.PolicyOption{filter.WithMaxOperations(1)}, filter.ErrMaxOperationsExceeded},
		{"list depth", `x in [[[1]]]`, []filter.PolicyOption{filter.WithMaxDepth(2)}, filter.ErrMaxDepthExceeded},
		{"exact budget", `x == 1`, []filter.PolicyOption{filter.WithMaxOperations(3), filter.WithMaxDepth(1)}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := append(slices.Clip(tc.options), filter.WithAnyField(true))
			mo, err := mongo.NewFilterTranslator(filtertest.MustPolicy(t, opts...))
			require.NoError(t, err)

			_, err = mo.Translate(filtertest.MustParseFilter(t, tc.expr))
			if tc.want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.want)
			}
		})
	}
}

// TestMongoFieldComparison: a comparison of two fields is an aggregation comparison under $expr.
func TestMongoFieldComparison(t *testing.T) {
	t.Parallel()

	tr, err := mongo.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)

	q, err := tr.Translate(filtertest.MustParseFilter(t, `a == b`))
	require.NoError(t, err)
	require.Equal(t, bson.M{"$expr": bson.M{"$eq": bson.A{
		bson.M{"$ifNull": bson.A{"$a", nil}},
		bson.M{"$ifNull": bson.A{"$b", nil}},
	}}}, q)
}

// TestMongoSizeLiteralCannotReferenceHiddenField: a size() literal is no field reference (filter
// TestMongoSizeLiteralCannotReferenceHiddenField).
func TestMongoSizeLiteralCannotReferenceHiddenField(t *testing.T) {
	t.Parallel()

	tr, err := mongo.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithUntrustedInput(true), filter.WithAllowedFields("public")))
	require.NoError(t, err)

	_, err = tr.Translate(filtertest.MustParseFilter(t, `public.size() > "$secret"`))
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
}

// TestQueryBackendsDynamicValues: a field on the value side translates to $expr; a matches() pattern stays literal-only
// (filter TestQueryBackendsRejectDynamicValues covers the backends that reject all three).
func TestQueryBackendsDynamicValues(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{`a == b`, `a in [b]`, `a.contains(b)`, `a.matches(b)`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			mo, err := mongo.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
			require.NoError(t, err)

			_, err = mo.Translate(filtertest.MustParseFilter(t, expr))
			if expr == `a.matches(b)` {
				require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
				return
			}

			require.NoError(t, err)
		})
	}
}

// translateUntrusted translates node with the options of the filter package's translatorBackends table.
func translateUntrusted(t *testing.T, node filter.Node) error {
	t.Helper()

	tr, err := mongo.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithUntrustedInput(true), filter.WithAllowedFields("name")))
	require.NoError(t, err)

	_, err = tr.Translate(node)

	return err
}

// TestTranslators_BareLiteralIsNotAPredicate: a bare literal is not a filter clause (filter
// TestTranslators_BareLiteralIsNotAPredicate).
func TestTranslators_BareLiteralIsNotAPredicate(t *testing.T) {
	t.Parallel()

	p, err := parser.New(parser.WithNoCache(true))
	require.NoError(t, err)

	for name, expression := range map[string]string{
		"sql injection payload": `"'; DROP TABLE users; --"`,
		"tautology":             `"1=1 OR 1=1"`,
		"bare quote":            `"'"`,
		"plain string":          `"plain"`,
		"empty string":          `""`,
		"integer":               `123`,
		"boolean":               `true`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node, err := p.Parse(t.Context(), expression)
			require.NoError(t, err)
			require.ErrorIs(t, translateUntrusted(t, node), filter.ErrInvalidExpression, "a bare literal was accepted as a filter clause")
		})
	}
}

// TestTranslators_LiteralFieldIsRejected: a string literal does not stand in for a field name (filter
// TestTranslators_LiteralFieldIsRejected).
func TestTranslators_LiteralFieldIsRejected(t *testing.T) {
	t.Parallel()

	p, err := parser.New(parser.WithNoCache(true))
	require.NoError(t, err)

	for name, expression := range map[string]string{
		"disallowed field":    `"secret" == 1`,
		"mongo operator":      `"$where" == "return true"`,
		"meili clause":        `"name = 1 OR secret" == 1`,
		"redis selector":      `"name:x | @secret" == "y"`,
		"membership":          `"secret" in ["a"]`,
		"literal call target": `"secret".startsWith("a")`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node, err := p.Parse(t.Context(), expression)
			require.NoError(t, err)
			require.ErrorIs(t, translateUntrusted(t, node), filter.ErrInvalidExpression, "a string literal was accepted as a field reference")
		})
	}
}

// TestTranslators_CallWithoutTargetIsRejected: a call without a target is rejected, not dereferenced (filter
// TestTranslators_CallWithoutTargetIsRejected).
func TestTranslators_CallWithoutTargetIsRejected(t *testing.T) {
	t.Parallel()

	p, err := parser.New(parser.WithNoCache(true))
	require.NoError(t, err)

	for _, expression := range []string{"contains()", "startsWith()", "endsWith()", "size()"} {
		t.Run(expression, func(t *testing.T) {
			t.Parallel()

			node, err := p.Parse(t.Context(), expression)
			if err != nil {
				t.Skipf("the parser rejects %q on its own: %v", expression, err)
			}

			require.Error(t, translateUntrusted(t, node), "%q must be rejected, not translated", expression)
		})
	}
}

// TestTranslators_SizeCallFormsAgree: size(name) and name.size() translate alike (filter TestTranslators_SizeCallFormsAgree).
func TestTranslators_SizeCallFormsAgree(t *testing.T) {
	t.Parallel()

	p, err := parser.New(parser.WithNoCache(true))
	require.NoError(t, err)

	method, err := p.Parse(t.Context(), `name.size() > 3`)
	require.NoError(t, err)
	global, err := p.Parse(t.Context(), `size(name) > 3`)
	require.NoError(t, err)

	methodErr := translateUntrusted(t, method)
	globalErr := translateUntrusted(t, global)

	if methodErr == nil {
		require.NoError(t, globalErr)

		return
	}

	require.Error(t, globalErr)
	require.Equal(t, methodErr.Error(), globalErr.Error())
}

// TestUntrustedInput_RequiresAllowlist: untrusted input requires an allow-list (filter TestUntrustedInput_RequiresAllowlist).
func TestUntrustedInput_RequiresAllowlist(t *testing.T) {
	t.Parallel()

	node := filtertest.MustParseFilter(t, `name == "x"`)

	// Untrusted + no allow-list -> constructor rejects.
	_, err := filter.NewPolicy(filter.WithUntrustedInput(true))
	require.ErrorIs(t, err, filter.ErrAllowlistRequired)

	// Untrusted + allow-list -> constructs and translates.
	trans, err := mongo.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithUntrustedInput(true), filter.WithAllowedFields("name")))
	require.NoError(t, err)
	_, err = trans.Translate(node)
	require.NoError(t, err)

	// Trusted + no allow-list -> permissive only with the explicit WithAnyField opt-out.
	trans, err = mongo.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)
	_, err = trans.Translate(node)
	require.NoError(t, err)
}

// TestUntrustedInput_EmptyAllowlist: an empty allow-list is no allow-list (filter TestUntrustedInput_EmptyAllowlist).
func TestUntrustedInput_EmptyAllowlist(t *testing.T) {
	t.Parallel()

	_, err := filter.NewPolicy(filter.WithUntrustedInput(true), filter.WithAllowedFields())
	require.ErrorIs(t, err, filter.ErrAllowlistRequired)

	var empty []string

	_, err = filter.NewPolicy(filter.WithUntrustedInput(true), filter.WithAllowedFields(empty...))
	require.ErrorIs(t, err, filter.ErrAllowlistRequired)
}

// TestAllowlistRequiredByDefault: the translator needs a policy, and a policy needs an allow-list unless WithAnyField opts
// out (filter TestAllowlistRequiredByDefault).
func TestAllowlistRequiredByDefault(t *testing.T) {
	t.Parallel()

	_, err := mongo.NewFilterTranslator(nil)
	require.ErrorIs(t, err, filter.ErrPolicyRequired)
	_, err = filter.NewPolicy()
	require.ErrorIs(t, err, filter.ErrAllowlistRequired)
	_, err = filter.NewPolicy(filter.WithAllowedFields())
	require.ErrorIs(t, err, filter.ErrAllowlistRequired)

	// WithAnyField does not lift the allow-list requirement of untrusted input.
	_, err = filter.NewPolicy(filter.WithAnyField(true), filter.WithUntrustedInput(true))
	require.ErrorIs(t, err, filter.ErrAllowlistRequired)

	hidden := filtertest.MustParseFilter(t, `passwordHash.startsWith("a")`)

	trans, err := mongo.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("name")))
	require.NoError(t, err)
	_, err = trans.Translate(hidden)
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)

	// An allow-list still applies when WithAnyField is also given.
	trans, err = mongo.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithAllowedFields("name")))
	require.NoError(t, err)
	_, err = trans.Translate(hidden)
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)

	trans, err = mongo.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)))
	require.NoError(t, err)
	_, err = trans.Translate(hidden)
	require.NoError(t, err)
}

// TestUntrustedInput_RejectsMatches: matches() under untrusted input and the safe-regex subset (filter
// TestUntrustedInput_RejectsMatches).
func TestUntrustedInput_RejectsMatches(t *testing.T) {
	t.Parallel()

	untrusted := []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithAllowedFields("name")}
	safe := append(slices.Clip(untrusted), filter.WithSafeRegex(true))

	for _, tc := range []struct {
		name string
		expr string
		opts []filter.PolicyOption
		want error
	}{
		{"untrusted literal", `name.matches("abc")`, untrusted, filter.ErrFunctionNotAllowed},
		{"untrusted catastrophic", `name.matches("(a|aa)+$")`, untrusted, filter.ErrFunctionNotAllowed},
		{"untrusted nested", `name.startsWith("a") || name.matches("^x")`, untrusted, filter.ErrFunctionNotAllowed},
		{"safe literal", `name.matches("abc")`, safe, nil},
		{"safe case-insensitive literal", `name.matches("(?i)a\\.b")`, safe, nil},
		{"safe anchored classes", `name.matches("^[a-z]+-[0-9]{2,4}.*x$")`, safe, nil},
		{"safe alternation of literals", `name.matches("^(foo|bar)$")`, safe, nil},
		{"safe rejects repeated alternation", `name.matches("(a|aa)+$")`, safe, filter.ErrInvalidRegex},
		{"safe rejects nested quantifier", `name.matches("(a+)+b")`, safe, filter.ErrInvalidRegex},
		{"safe rejects repeated group", `name.matches("(ab)*c")`, safe, filter.ErrInvalidRegex},
		{"safe rejects bounded repeat of group", `name.matches("(a.*){5}")`, safe, filter.ErrInvalidRegex},
		{"trusted holds the safe subset", `name.matches("(a|aa)+$")`, []filter.PolicyOption{filter.WithAllowedFields("name")}, filter.ErrInvalidRegex},
		{"trusted unsafe opt-out", `name.matches("(a|aa)+$")`, []filter.PolicyOption{filter.WithAllowedFields("name"), filter.WithUnsafeRegex(true)}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mo, err := mongo.NewFilterTranslator(filtertest.MustPolicy(t, tc.opts...))
			require.NoError(t, err)

			_, err = mo.Translate(filtertest.MustParseFilter(t, tc.expr))
			if tc.want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.want)
			}
		})
	}
}

// TestUntrustedInput_SafeRegexKeepsTextSearch: TextSearch still translates under untrusted input with the safe-regex subset
// (filter TestUntrustedInput_SafeRegexKeepsTextSearch).
func TestUntrustedInput_SafeRegexKeepsTextSearch(t *testing.T) {
	t.Parallel()

	p, err := parser.New(parser.WithCustomFunctions(map[string]filter.CustomFunction{"search": filterfuncs.TextSearch([]string{"name"})}))
	require.NoError(t, err)

	node, err := p.Parse(t.Context(), `search("a+(b)*")`)
	require.NoError(t, err)

	mo, err := mongo.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithUntrustedInput(true), filter.WithAllowedFields("name"), filter.WithSafeRegex(true)))
	require.NoError(t, err)

	_, err = mo.Translate(node)
	require.NoError(t, err)
}
