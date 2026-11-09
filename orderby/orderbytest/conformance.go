// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package orderbytest

import (
	"errors"
	"slices"
	"testing"

	"github.com/altessa-s/go-aip/orderby"
)

// TranslateFunc translates a spec with one configured translator and renders the backend output as a string, so that the
// conformance suite can compare it with an expectation. Rendering is the backend's choice (for example "created_at:desc" for
// Meilisearch, or the ids of [ConformanceRecords] in sorted order for an in-memory comparator); an empty output must render "".
type TranslateFunc func(orderby.Spec) (string, error)

// Outcome is the expected result of one conformance case on one backend: the rendered output Want, or an error matching Err with
// [errors.Is].
type Outcome struct {
	Err  error
	Want string
}

// Backend is one order translator under the conformance suite.
type Backend struct {
	// New builds the translator under test bound to policy with the storage options opts ([orderby.TranslatorOption], passed
	// through the translator's WithOrderOptions) and returns its rendered Translate. A nil policy must fail with
	// [orderby.ErrPolicyRequired].
	New func(policy *orderby.Policy, opts ...orderby.TranslatorOption) (TranslateFunc, error)
	// Expect holds the outcome of every backend-specific case, by case name ([ConformanceCases]). A missing or unknown name fails
	// the suite, so a case added to the suite makes every backend declare its behavior.
	Expect map[string]Outcome
}

// Case names of the conformance suite whose outcome depends on the backend. [Backend.Expect] must hold exactly these names.
const (
	CaseEmpty             = "empty"
	CaseAscending         = "ascending"
	CaseDescending        = "descending"
	CaseMultipleKeys      = "multiple keys"
	CaseNestedPath        = "nested path"
	CaseFieldMapping      = "field mapping"
	CasePrefixMapping     = "prefix mapping"
	CaseDefaultOrder      = "default order"
	CaseTiebreaker        = "tiebreaker"
	CaseTiebreakerPresent = "tiebreaker already present"
	CaseAscNullsFirst     = "asc nulls first"
	CaseAscNullsLast      = "asc nulls last"
	CaseDescNullsFirst    = "desc nulls first"
	CaseDescNullsLast     = "desc nulls last"
	CaseCollation         = "collation"
)

// Field names of [ConformanceRecords] the cases refer to.
const (
	fieldName    = "name"
	fieldAge     = "age"
	fieldCreated = "created_at"
	fieldAddress = "address"
)

// conformanceCase is one row of the suite: a policy, storage options and a spec. err is the outcome every backend shares; a
// case without one takes its outcome from [Backend.Expect].
type conformanceCase struct {
	err    error
	name   string
	policy []orderby.PolicyOption
	opts   []orderby.TranslatorOption
	spec   orderby.Spec
}

// key builds a one-key spec.
func key(name string, dir orderby.Direction, nulls orderby.NullOrder, collation string) orderby.Spec {
	return orderby.Spec{Keys: []orderby.Key{{Name: name, Direction: dir, Nulls: nulls, Collation: collation}}}
}

// keys builds a spec of plain keys; a name followed by "desc" is descending.
func keys(names ...string) orderby.Spec {
	var spec orderby.Spec

	for _, n := range names {
		if n == "desc" {
			spec.Keys[len(spec.Keys)-1].Direction = orderby.DirectionDescending
			continue
		}

		spec.Keys = append(spec.Keys, orderby.Key{Name: n})
	}

	return spec
}

// conformanceCases returns the rows of the suite. Field names match [ConformanceRecords].
func conformanceCases() []conformanceCase {
	asc, desc := orderby.DirectionAscending, orderby.DirectionDescending
	tiebreaker := orderby.WithTiebreaker(orderby.Key{Name: "id"})

	return []conformanceCase{
		{name: CaseEmpty},
		{name: CaseAscending, spec: keys(fieldName)},
		{name: CaseDescending, spec: keys(fieldName, "desc")},
		{name: CaseMultipleKeys, spec: keys(fieldAge, "desc", fieldName)},
		// A dotted key is declared exactly: an SQL dialect reads the dot as a table qualifier and refuses one a wildcard
		// admits.
		{name: CaseNestedPath, spec: keys("address.city"), policy: []orderby.PolicyOption{orderby.WithAllowedFields("address.city")}},
		{
			name: CaseFieldMapping, spec: keys("createdAt", "desc"),
			opts: []orderby.TranslatorOption{orderby.WithFieldMapping(map[string]string{"createdAt": fieldCreated})},
		},
		{
			name: CasePrefixMapping, spec: keys("addr.city"), policy: []orderby.PolicyOption{orderby.WithAllowedFields("addr.city")},
			opts: []orderby.TranslatorOption{orderby.WithFieldPrefixMapping(map[string]string{"addr.": "address."})},
		},
		{name: CaseDefaultOrder, policy: []orderby.PolicyOption{orderby.WithDefaultOrder(orderby.Key{Name: fieldAge, Direction: desc})}},
		{name: CaseTiebreaker, spec: keys(fieldName), opts: []orderby.TranslatorOption{tiebreaker}},
		{name: CaseTiebreakerPresent, spec: keys("id", "desc"), opts: []orderby.TranslatorOption{tiebreaker}},
		{name: CaseAscNullsFirst, spec: key(fieldAge, asc, orderby.NullsFirst, "")},
		{name: CaseAscNullsLast, spec: key(fieldAge, asc, orderby.NullsLast, "")},
		{name: CaseDescNullsFirst, spec: key(fieldAge, desc, orderby.NullsFirst, "")},
		{name: CaseDescNullsLast, spec: key(fieldAge, desc, orderby.NullsLast, "")},
		{name: CaseCollation, spec: key(fieldName, asc, orderby.NullsDefault, "C")},

		// Outcomes every backend shares.
		{
			name: "field not allowed", err: orderby.ErrFieldNotAllowed, spec: keys(fieldAge),
			policy: []orderby.PolicyOption{orderby.WithUntrustedInput(true), orderby.WithAllowedFields(fieldName)},
		},
		{name: "denied api field", err: orderby.ErrFieldNotAllowed, spec: keys("secret"), policy: []orderby.PolicyOption{orderby.WithDeniedFields("secret")}},
		{
			name: "denied storage field", err: orderby.ErrFieldNotAllowed, spec: keys("alias"),
			opts: []orderby.TranslatorOption{
				orderby.WithFieldMapping(map[string]string{"alias": "secret"}), orderby.WithDeniedStorageFields("secret"),
			},
		},
		{
			name: "duplicate storage key", err: orderby.ErrDuplicateKey, spec: keys("a", "b"),
			opts: []orderby.TranslatorOption{orderby.WithFieldMapping(map[string]string{"a": fieldName, "b": fieldName})},
		},
		{name: "unknown null placement", err: orderby.ErrUnsupportedOrdering, spec: key(fieldAge, asc, orderby.NullsLast+1, "")},
		{name: "invalid direction", err: orderby.ErrInvalidDirection, spec: key(fieldAge, desc+1, orderby.NullsDefault, "")},
		{name: "collation injection", err: orderby.ErrUnsupportedOrdering, spec: key(fieldName, asc, orderby.NullsDefault, `C" DESC; --`)},
	}
}

// ConformanceCases returns the names of the backend-specific cases, the keys [Backend.Expect] must hold.
func ConformanceCases() []string {
	var names []string

	for _, c := range conformanceCases() {
		if c.err == nil {
			names = append(names, c.name)
		}
	}

	return names
}

// ConformanceRecords returns the records the suite's field names refer to, for backends that render an output by ordering data
// (an in-memory comparator): ids "1" to "5", with name, age (null in one record and missing in another), created_at and
// address.city. Every call returns fresh records.
func ConformanceRecords() []map[string]any {
	//nolint:mnd,goconst // fixture values
	return []map[string]any{
		{"id": "1", fieldName: "carol", fieldAge: 30, fieldCreated: 300, fieldAddress: map[string]any{"city": "Oslo"}},
		{"id": "2", fieldName: "alice", fieldAge: nil, fieldCreated: 100, fieldAddress: map[string]any{"city": "Bern"}},
		{"id": "3", fieldName: "bob", fieldAge: 25.5, fieldCreated: 500, fieldAddress: map[string]any{"city": "Lima"}},
		{"id": "4", fieldName: "dave", fieldCreated: 200, fieldAddress: map[string]any{"city": "Accra"}},
		{"id": "5", fieldName: "erin", fieldAge: 30, fieldCreated: 400, fieldAddress: map[string]any{"city": "Kyiv"}},
	}
}

// RunConformance runs the orderby conformance suite against b: the shared policy and storage-option semantics every translator
// inherits from [orderby.TranslatorContext] (allow-list, denied fields, mapping, duplicate storage keys, default order,
// tie-breakers), and the backend capability rows (multiple keys, null placement, collation) whose outcome b declares in Expect.
// An unsupported capability must fail with [orderby.ErrUnsupportedOrdering] (or an error wrapping it, such as
// [orderby.ErrTooManySortKeys]). The suite also checks that a nil policy fails with [orderby.ErrPolicyRequired] and that
// Translate leaves the caller's spec unchanged.
func RunConformance(t *testing.T, b Backend) {
	t.Helper()

	cases := conformanceCases()
	checkExpectations(t, b, cases)

	if _, err := b.New(nil); !errors.Is(err, orderby.ErrPolicyRequired) {
		t.Errorf("New(nil policy) error = %v, want %v", err, orderby.ErrPolicyRequired)
	}

	for _, c := range cases {
		want := Outcome{Err: c.err}
		if c.err == nil {
			want = b.Expect[c.name]
		}

		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			translate, err := b.New(MustPolicy(t, c.policy...), c.opts...)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			spec := c.spec.Clone()

			got, err := translate(spec)
			if !slices.Equal(spec.Keys, c.spec.Keys) {
				t.Errorf("Translate() modified the spec: %v, want %v", spec, c.spec)
			}

			if want.Err != nil {
				if !errors.Is(err, want.Err) {
					t.Fatalf("Translate(%v) = %q, %v; want error %v", c.spec, got, err, want.Err)
				}

				if got != "" {
					t.Errorf("Translate(%v) output = %q with an error, want empty", c.spec, got)
				}

				return
			}

			if err != nil || got != want.Want {
				t.Fatalf("Translate(%v) = %q, %v; want %q", c.spec, got, err, want.Want)
			}
		})
	}
}

// checkExpectations fails t when b.Expect does not hold exactly the backend-specific cases.
func checkExpectations(t *testing.T, b Backend, cases []conformanceCase) {
	t.Helper()

	names := make(map[string]bool, len(cases))
	for _, c := range cases {
		names[c.name] = c.err == nil
	}

	for name := range b.Expect {
		if specific, ok := names[name]; !ok || !specific {
			t.Errorf("Expect[%q]: not a backend-specific conformance case", name)
		}
	}

	for name, specific := range names {
		if _, ok := b.Expect[name]; specific && !ok {
			t.Errorf("Expect[%q]: missing outcome", name)
		}
	}
}
