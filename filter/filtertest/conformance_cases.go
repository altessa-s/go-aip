// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filtertest

import (
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/functions"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
	regexanchor "github.com/altessa-s/go-aip/filter/regexanchor"
)

// Field names of [ConformanceRecords] the cases refer to (API names; without a field mapping also the storage names).
const (
	fieldID       = "id"
	fieldName     = "name"
	fieldAge      = "age"
	fieldScore    = "score"
	fieldActive   = "active"
	fieldCreated  = "created"
	fieldTags     = "tags"
	fieldStatus   = "status"
	fieldAddress  = "address"
	fieldCity     = "address.city"
	fieldData     = "data"
	fieldNickname = "nickname"
	fieldNote     = "note"
	fieldAlias    = "alias"
	fieldSecret   = "secret"
	fieldPrice    = "price"
)

// conformanceCase is one row of the suite: a policy, translator options and a filter. err is the outcome every backend shares; a
// case without one takes its outcome from [Backend.Expect].
type conformanceCase struct {
	err  error
	name string
	// expr is parsed with parser (plus WithNoCache); an empty expr with a nil node is the nil filter.
	expr   string
	parser []filterparser.Option
	// node builds a filter the parser cannot produce; nodeDesc describes it.
	node     func() filter.Node
	nodeDesc string
	// decimal declares price as [filter.FieldKindDecimal] (and allows it); policy adds options after the base policy.
	decimal bool
	policy  []filter.PolicyOption
	opts    []filter.TranslatorOption
}

// denySecret is the field access policy of the suite: it denies secret (and its subfields) although the allow-list names it.
type denySecret struct{}

// Allows implements [fieldpolicy.Access].
func (denySecret) Allows(field string) bool {
	return field != fieldSecret && !strings.HasPrefix(field, fieldSecret+".")
}

// ConformanceFieldKinds returns the declared kinds of the fields of [ConformanceRecords] in the suite's base policy (the element
// kind for the array field tags). price is declared [filter.FieldKindDecimal] only by the decimal cases; a backend that needs
// a schema (RediSearch) builds it from these kinds.
func ConformanceFieldKinds() map[string]filter.FieldKind {
	return map[string]filter.FieldKind{
		fieldName: filter.FieldKindString, fieldAge: filter.FieldKindInt, fieldScore: filter.FieldKindFloat,
		fieldActive: filter.FieldKindBool, fieldCreated: filter.FieldKindTimestamp, fieldTags: filter.FieldKindString,
		fieldStatus: filter.FieldKindInt, fieldCity: filter.FieldKindString, fieldData: filter.FieldKindBytes,
		fieldNickname: filter.FieldKindString, fieldNote: filter.FieldKindString, fieldAlias: filter.FieldKindString,
		fieldSecret: filter.FieldKindString, fieldPrice: filter.FieldKindDecimal,
	}
}

// ConformanceArrayFields returns the fields the suite's policy declares with [filter.WithArrayFields].
func ConformanceArrayFields() []string { return []string{fieldTags} }

// policyOptions returns the options of the case's policy: untrusted input with an allow-list of the record fields, declared
// kinds, the array field tags, the enum status (0, 1, 2), safe regular expressions and a field access policy denying secret.
func (c conformanceCase) policyOptions() []filter.PolicyOption {
	kinds := ConformanceFieldKinds()
	allowed := []string{
		fieldName, fieldAge, fieldScore, fieldActive, fieldCreated, fieldTags, fieldStatus, fieldCity, fieldData, fieldNickname,
		fieldNote, fieldAlias, fieldSecret,
	}

	if c.decimal {
		allowed = append(allowed, fieldPrice)
	} else {
		delete(kinds, fieldPrice)
	}

	opts := make([]filter.PolicyOption, 0, len(c.policy)+7) //nolint:mnd // the base options below
	opts = append(opts,
		filter.WithUntrustedInput(true),
		filter.WithAllowedFields(allowed...),
		filter.WithFieldTypes(kinds),
		filter.WithArrayFields(ConformanceArrayFields()...),
		filter.WithEnumValues(map[string][]int64{fieldStatus: {0, 1, 2}}),
		filter.WithSafeRegex(true),
		filter.WithFieldAccess(denySecret{}),
	)

	return append(opts, c.policy...)
}

// ConformanceRecords returns the records the suite's filters select from, for backends that run their output in-process: ids
// "1" to "5", every field of [ConformanceFieldKinds] in its Go form (int64, float64, bool, time.Time, []byte, []any of strings,
// decimal.Decimal, a nested address document). note is null in record 2 and absent in record 3; tags is empty in record 3
// and absent in record 5; name and address.city are equal in record 3. Every call returns fresh records.
func ConformanceRecords() []map[string]any {
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

	//nolint:goconst,mnd // fixture values
	records := []map[string]any{
		{
			fieldID: "1", fieldName: "alice", fieldAge: int64(30), fieldScore: 1.5, fieldActive: true, fieldCreated: day(2024, 3, 1),
			fieldTags: []any{"red", "blue"}, fieldStatus: int64(1), fieldAddress: map[string]any{"city": "Oslo"},
			fieldData: []byte("ab"), fieldNickname: "al", fieldNote: "x", fieldSecret: "s1", fieldPrice: decimal.RequireFromString("10.50"),
		},
		{
			fieldID: "2", fieldName: "bob", fieldAge: int64(25), fieldScore: 2.5, fieldActive: false, fieldCreated: day(2023, 6, 15),
			fieldTags: []any{"green"}, fieldStatus: int64(0), fieldAddress: map[string]any{"city": "Bern"},
			fieldData: []byte("x"), fieldNickname: "bobby", fieldNote: nil, fieldSecret: "s2", fieldPrice: decimal.RequireFromString("3.1"),
		},
		{
			fieldID: "3", fieldName: "carol", fieldAge: int64(41), fieldScore: 0.5, fieldActive: true, fieldCreated: day(2025, 1, 1),
			fieldTags: []any{}, fieldStatus: int64(2), fieldAddress: map[string]any{"city": "carol"},
			fieldData: []byte(""), fieldNickname: "carol", fieldSecret: "s3", fieldPrice: decimal.RequireFromString("7"),
		},
		{
			fieldID: "4", fieldName: "Dave", fieldAge: int64(19), fieldScore: 3.0, fieldActive: false,
			fieldCreated: time.Date(2022, 12, 31, 23, 59, 59, 0, time.UTC),
			fieldTags:    []any{"red"}, fieldStatus: int64(1), fieldAddress: map[string]any{"city": "Lima"},
			fieldData: []byte("dave"), fieldNickname: "D", fieldNote: "", fieldSecret: "s4", fieldPrice: decimal.RequireFromString("0.1"),
		},
		{
			fieldID: "5", fieldName: "erin", fieldAge: int64(30), fieldScore: 1.5, fieldActive: true, fieldCreated: day(2024, 3, 1),
			fieldStatus: int64(2), fieldAddress: map[string]any{"city": "Kyiv"},
			fieldData: []byte("ab"), fieldNickname: "e", fieldNote: "y", fieldSecret: "s5", fieldPrice: decimal.RequireFromString("10.5"),
		},
	}

	return records
}

// conformanceCases returns the rows of the suite. Field names match [ConformanceRecords].
//
//nolint:goconst,mnd // one table of cases and their fixture values
func conformanceCases() []conformanceCase {
	textSearch := map[string]filter.CustomFunction{
		"search":  functions.TextSearch([]string{fieldName, fieldNickname}),
		"isearch": functions.TextSearch([]string{fieldName}, functions.WithOperator(filter.OpMatches), functions.WithCaseInsensitive(true)),
	}
	mapping := []filter.TranslatorOption{filter.WithFieldMapping(map[string]string{fieldAlias: fieldNickname})}
	cityMapping := []filter.TranslatorOption{filter.WithFieldMapping(map[string]string{fieldCity: "location.city"})}

	cases := []conformanceCase{
		// Comparisons.
		{name: "equal string", expr: `name == "alice"`},
		{name: "not equal string", expr: `name != "alice"`},
		{name: "less int", expr: `age < 30`},
		{name: "less or equal int", expr: `age <= 30`},
		{name: "greater float", expr: `score > 1.5`},
		{name: "greater or equal float", expr: `score >= 1.5`},
		{name: "float literal on int field", expr: `age > 24.5`},
		{name: "int literal on float field", expr: `score == 3`},
		{name: "negative literal", expr: `age > -1`},
		{name: "ordered string", expr: `name < "c"`},
		{name: "equal bool", expr: `active == true`},
		{name: "bare bool", expr: `active`},
		{name: "negated bare bool", expr: `!active`},
		{name: "enum value", expr: `status == 1`},
		{name: "nested field", expr: `address.city == "Oslo"`},
		{name: "nested field mapping", expr: `address.city == "Oslo"`, opts: cityMapping},
		{name: "field mapping", expr: `alias == "al"`, opts: mapping},

		// Presence and null.
		{name: "has nested", expr: `has(address.city)`},
		{name: "equal null", expr: `note == null`},
		{name: "not equal null", expr: `note != null`},
		{name: "ordered null", expr: `age > null`},
		{name: "ordered nullable field", expr: `note < "m"`},
		{name: "equal empty string", expr: `note == ""`},

		// Membership.
		{name: "in ints", expr: `age in [25, 30]`},
		{name: "in strings", expr: `name in ["alice", "carol"]`},
		{name: "in empty list", expr: `age in []`},
		{name: "in with null", expr: `note in ["x", null]`},
		{name: "not in", expr: `!(age in [25, 30])`},
		{name: "in non-array field", expr: `"alice" in name`},

		// String functions.
		{name: "contains", expr: `name.contains("ar")`},
		{name: "starts with", expr: `name.startsWith("ca")`},
		{name: "ends with", expr: `name.endsWith("ob")`},
		{name: "contains wildcard characters", expr: `name.contains("%_*")`},
		{name: "starts with wildcard characters", expr: `name.startsWith("a%")`},
		{name: "contains empty string", expr: `name.contains("")`},
		{name: "contains field", expr: `name.contains(nickname)`},
		{name: "starts with field", expr: `name.startsWith(nickname)`},
		{name: "ends with field", expr: `name.endsWith(address.city)`},
		{name: "case-sensitive contains", expr: `name.contains("dave")`},
		{name: "text search", expr: `search("ob")`, parser: []filterparser.Option{filterparser.WithCustomFunctions(textSearch)}},
		{name: "text search case-insensitive", expr: `isearch("DAV")`, parser: []filterparser.Option{filterparser.WithCustomFunctions(textSearch)}},

		// Regular expressions.
		{name: "matches", expr: `name.matches("^[ab]")`},
		{name: "matches end anchor", expr: `name.matches("ol$")`},

		// size() and substring().
		{name: "size of string", expr: `name.size() > 4`},
		{name: "size function", expr: `size(name) == 3`},
		{name: "size of array", expr: `tags.size() == 1`},
		{name: "size of empty array", expr: `size(tags) == 0`},
		{name: "substring", expr: `name.substring(0, 2) == "ca"`},
		{name: "substring from", expr: `name.substring(1) == "ob"`},
		{name: "substring ordered", expr: `name.substring(0, 1) < "c"`},
		{name: "substring reversed range", expr: `name.substring(2, 1) == "x"`},

		// Timestamps, durations, bytes.
		{name: "timestamp greater", expr: `created > timestamp("2024-01-01T00:00:00Z")`},
		{name: "timestamp equal", expr: `created == timestamp("2024-03-01T00:00:00Z")`},
		{name: "timestamp fraction", expr: `created < timestamp("2022-12-31T23:59:59.5Z")`},
		{
			name: "duration literal", nodeDesc: "created > time.Duration(1h)",
			node: func() filter.Node {
				return &filter.BinaryOpNode{Op: filter.OpGT, Left: &filter.IdentNode{Name: fieldCreated}, Right: &filter.LiteralNode{Value: time.Hour}}
			},
		},
		{name: "bytes equal", expr: `data == b"ab"`},
		{name: "bytes ordered", expr: `data < b"b"`},

		// Exact decimals (price declared FieldKindDecimal).
		{name: "decimal greater", expr: `price > 5.25`, decimal: true},
		{name: "decimal equal", expr: `price == 0.1`, decimal: true},
		{name: "decimal trailing zeros", expr: `price == 10.5`, decimal: true},
		{name: "decimal string literal", expr: `price >= "10.50"`, decimal: true},
		{name: "decimal in", expr: `price in [3.1, 7]`, decimal: true},
		{name: "decimal not a number", expr: `price == "abc"`, decimal: true},

		// Array fields.
		{name: "array equal", expr: `tags == "red"`},
		{name: "array not equal", expr: `tags != "red"`},
		{name: "element in array", expr: `"red" in tags`},
		{name: "array in list", expr: `tags in ["green", "blue"]`},
		{name: "array starts with", expr: `tags.startsWith("gr")`},
		{name: "array ordered", expr: `tags < "c"`},
		{name: "array equal null", expr: `tags == null`},

		// Logic.
		{name: "and", expr: `name == "alice" && age > 20`},
		{name: "or", expr: `age < 20 || score > 2.0`},
		{name: "and binds tighter than or", expr: `name == "bob" || name == "alice" && age > 26`},
		{name: "parentheses", expr: `(name == "bob" || name == "alice") && age > 26`},
		{name: "not", expr: `!(name == "alice")`},
		{name: "not and", expr: `!(active && age > 26)`},
		{name: "not or", expr: `!(name == "alice" || age < 20)`},
		{name: "double negation", expr: `!!active`},
		{name: "bare literal", expr: `true`},

		// Three-valued logic: a predicate on a null or absent value that SQL finds unknown (note is null in record 2 and absent in
		// record 3) is absorbed by && and || in either order, stays unknown under !, and an unknown filter selects nothing.
		{name: "unknown or true", expr: `note < "m" || age > 0`},
		{name: "true or unknown", expr: `age > 0 || note < "m"`},
		{name: "unknown or false", expr: `note < "m" || age < 0`},
		{name: "unknown and false", expr: `note < "m" && age < 0`},
		{name: "false and unknown", expr: `age < 0 && note < "m"`},
		{name: "not unknown", expr: `!(note < "m")`},
		{name: "not unknown or false", expr: `!(note < "m" || name == "zzz")`},
		{name: "not unknown and false", expr: `!(note < "m" && age < 0)`},
		{name: "not size of null", expr: `!(size(note) == 0)`},
		{name: "not contains null argument", expr: `!name.contains(note)`},
		{name: "not substring out of range", expr: `!(name.substring(0, 4) == "x")`},
		{name: "nil filter"},

		// Field operands.
		{name: "field equal field", expr: `name == address.city`},
		{name: "field less field", expr: `name < nickname`},
		{name: "field in list", expr: `name in [address.city, "bob"]`},
		{name: "field kind mismatch", expr: `name == age`},

		// Policy rules every translator inherits.
		{name: "field not allowed", err: filter.ErrFieldNotAllowed, expr: `unknown == 1`},
		{name: "denied field", err: filter.ErrFieldNotAllowed, expr: `secret == "s1"`},
		{name: "denied field in function", err: filter.ErrFieldNotAllowed, expr: `name.contains(secret)`},
		{name: "enum value not allowed", err: filter.ErrEnumValueNotAllowed, expr: `status == 9`},
		{name: "literal kind mismatch", err: filter.ErrFieldTypeMismatch, expr: `age == "x"`},
		{name: "element kind mismatch", err: filter.ErrFieldTypeMismatch, expr: `1 in tags`},
		{name: "invalid regex", err: regexanchor.ErrInvalidRegex, expr: `name.matches("(")`},
		{name: "unsafe regex", err: regexanchor.ErrInvalidRegex, expr: `name.matches("(a|aa)+$")`},
		{name: "regex too long", err: regexanchor.ErrInvalidRegex, expr: `name.matches("abcdef")`, policy: []filter.PolicyOption{filter.WithMaxRegexLength(4)}},
		{name: "regex not allowed", err: filter.ErrFunctionNotAllowed, expr: `name.matches("^a")`, policy: []filter.PolicyOption{filter.WithSafeRegex(false)}},
		{
			name: "max depth", err: filter.ErrMaxDepthExceeded, expr: `!(!(!(!(name == "a"))))`,
			policy: []filter.PolicyOption{filter.WithMaxDepth(3)},
		},
		{
			name: "max operations", err: filter.ErrMaxOperationsExceeded, expr: `age == 1 || age == 2 || age == 3 || age == 4`,
			policy: []filter.PolicyOption{filter.WithMaxOperations(5)},
		},
	}

	return cases
}
