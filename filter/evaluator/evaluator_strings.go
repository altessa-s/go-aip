// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"unicode/utf8"

	"github.com/altessa-s/go-aip/internal/typename"

	filter "github.com/altessa-s/go-aip/filter"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// stringCallArgs resolves the target and the single string argument shared by
// the string-valued call functions (contains/startsWith/endsWith/matches). The
// target of an array field ([filter.WithArrayFields]) is the list of its elements
// ([evaluation.arrayElements]); any other target that is not a string yields
// handled=false with no error so the caller can return false — matching the
// permissive semantics on non-string fields. fn names the function in error
// messages.
func (e *evaluation) stringCallArgs(n *filter.CallNode, fn stringFunction) (target any, arg string, handled bool, err error) {
	if target, handled, err = e.stringTarget(n.Target); err != nil || !handled {
		return nil, "", false, err
	}

	if len(n.Args) != 1 {
		return nil, "", false, coreerrs.Wrapf(filter.ErrInvalidExpression, "%s requires exactly 1 argument", fn.label)
	}

	av, err := n.Args[0].Accept(e)
	if err != nil {
		return nil, "", false, err
	}

	if av == nil {
		return nil, "", false, fn.null
	}

	arg, ok := av.(string)
	if !ok {
		return nil, "", false, coreerrs.Wrapf(filter.ErrInvalidExpression, "%s argument must be a string", fn.label)
	}

	return target, arg, true, nil
}

// stringTarget resolves the target n of a string function: the elements of an array field ([evaluation.arrayElements]), or a
// string; handled is false for any other value. A field's options are read once.
func (e *evaluation) stringTarget(n filter.Node) (target any, handled bool, err error) {
	if ident, ok := n.(*filter.IdentNode); ok && ident != nil {
		f := e.config.Field(ident.Name)
		if f.IsArray() {
			elems, _, elemsErr := e.arrayElements(ident)
			return elems, elemsErr == nil, elemsErr
		}

		target, err = e.identValue(ident.Name, f)
	} else {
		target, err = n.Accept(e)
	}

	if err != nil {
		return nil, false, err
	}

	_, isString := target.(string)

	return target, isString, nil
}

// anyString reports whether target — a string, or a list whose string elements are tested one by one — satisfies holds.
func anyString(target any, holds func(string) bool) bool {
	switch t := target.(type) {
	case string:
		return holds(t)
	case []any:
		for _, el := range t {
			if s, ok := el.(string); ok && holds(s) {
				return true
			}
		}
	}

	return false
}

// evalStringFunc evaluates contains/startsWith/endsWith.
func (e *evaluation) evalStringFunc(n *filter.CallNode, fn func(string, string) bool) (any, error) {
	target, substr, handled, err := e.stringCallArgs(n, stringPredicate)
	if err != nil {
		return nil, err
	}

	if !handled {
		return false, nil
	}

	found := anyString(target, func(s string) bool { return e.budget.chargeScan(len(s)) && fn(s, substr) })

	return found, e.Err()
}

// compileRegex returns the compiled matches() pattern under the matches() policy ([filter.Policy.regexPattern]): the one a
// prepared filter compiled ([Prepared]), or the regex cache's, rather than through ValidateRegex, which would compile the
// pattern again on every evaluated record.
func (e *evaluation) compileRegex(pattern string) (filter.Regex, error) {
	if re, ok := e.regexes[pattern]; ok {
		return re, nil
	}

	return e.config.CompileRegex(pattern)
}

// evalMatches evaluates regex matching.
func (e *evaluation) evalMatches(n *filter.CallNode) (any, error) {
	target, pattern, handled, err := e.stringCallArgs(n, matchesFunction)
	if err != nil {
		return nil, err
	}

	if !handled {
		return false, nil
	}

	re, err := e.compileRegex(pattern)
	if err != nil {
		return nil, err
	}

	if s, ok := target.(string); ok { // anyString of a string, without the indirect call
		return e.budget.chargeRegex(len(s), len(pattern)) && re.MatchString(s), e.Err()
	}

	found := anyString(target, func(s string) bool { return e.budget.chargeRegex(len(s), len(pattern)) && re.MatchString(s) })

	return found, e.Err()
}

// evalSubstring returns target[start:end] (or target[start:] when end is
// omitted). Indices are 0-based and the range is half-open, matching the
// cel-go ext.Strings extension. start and end must be int64; both must lie
// within [0, len(runes)] and start must be <= end, otherwise the substring is
// a data-driven unknown ([unknownError]) wrapping [filter.ErrInvalidExpression],
// as the length guard of the SQL translators makes it. A null target or bound
// is unknown too.
//
// Indexing is over Unicode code points (runes), not bytes — same as
// cel-go ext.Strings — so a 3-rune Cyrillic prefix is substring(0, 3)
// regardless of the underlying UTF-8 byte length.
func (e *evaluation) evalSubstring(n *filter.CallNode) (any, error) {
	const (
		minSubstringArgs = 1
		maxSubstringArgs = 2
	)

	target, err := n.Target.Accept(e)
	if err != nil {
		return nil, err
	}

	if target == nil {
		return nil, errUnknownSubstringTarget
	}

	s, ok := target.(string)
	if !ok {
		return nil, coreerrs.Wrapf(filter.ErrInvalidExpression, "substring() target must be string, got %s", typename.Of(target))
	}

	if len(n.Args) < minSubstringArgs || len(n.Args) > maxSubstringArgs {
		return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "substring() requires 1 or 2 arguments")
	}

	start, err := e.evalIntArg(n.Args[0])
	if err != nil {
		return nil, err
	}
	// []rune(s) allocates 4×runeCount bytes per call. Fine for short
	// inputs (phone numbers, ids, names); rewrite as a
	// utf8.DecodeRuneInString walk if substring starts running on long
	// content fields under load. The data budget is charged for it.
	if !e.budget.chargeScan(utf8.UTFMax * len(s)) {
		return nil, e.Err()
	}

	runes := []rune(s)

	end := int64(len(runes))
	if len(n.Args) == maxSubstringArgs {
		end, err = e.evalIntArg(n.Args[1])
		if err != nil {
			return nil, err
		}
	}

	if start < 0 || end < 0 || start > end || end > int64(len(runes)) {
		// Data-driven: the SQL translators guard the comparison with the string's length, so it is unknown there.
		return nil, substringOutOfRange(start, end, len(runes))
	}

	return string(runes[start:end]), nil
}

// evalIntArg evaluates a Node and asserts the result is an int64. Used by
// integer-arg functions like substring.
func (e *evaluation) evalIntArg(n filter.Node) (int64, error) {
	v, err := n.Accept(e)
	if err != nil {
		return 0, err
	}

	if v == nil {
		return 0, errUnknownIntArgument
	}

	i, ok := v.(int64)
	if !ok {
		return 0, coreerrs.Wrapf(filter.ErrInvalidExpression, "argument must be int, got %s", typename.Of(v))
	}

	return i, nil
}
