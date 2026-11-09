// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package regexcache_test

import (
	"math"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter/internal/regexcache"
)

// Lengths and budget of the backtracking model.
const (
	modelShort  = 8
	modelLong   = 2 * modelShort
	modelBudget = 20_000_000
)

// backtracker is a model of a backtracking engine (PCRE, ICU): it runs a parsed pattern depth first, greedy repetitions first,
// and counts the steps. Every attempt fails, so it explores every way the pattern can match, as an engine does on a failing row.
// It runs the tree [unfold] builds from the source, not the one the Go parser builds: the parser folds (?:a|a) and (?:.|a) into
// one character and factors abc|abd into ab[cd], while an engine tries every branch.
type backtracker struct {
	in    []rune
	steps int
}

// run tries re at every start of in and returns the steps taken, or false once they pass the budget.
func (b *backtracker) run(re *syntax.Regexp) (int, bool) {
	for start := 0; start <= len(b.in); start++ {
		b.match(re, start, func(int) bool { return false })

		if b.steps > modelBudget {
			return b.steps, false
		}
	}

	return b.steps, true
}

// match matches re at in[i:] and calls next with the index past each match, most greedy first, until next reports true.
func (b *backtracker) match(re *syntax.Regexp, i int, next func(int) bool) bool {
	b.steps++
	if b.steps > modelBudget {
		return true
	}

	switch re.Op {
	case syntax.OpLiteral:
		for _, r := range re.Rune {
			if i >= len(b.in) || !sameRune(r, b.in[i], re.Flags&syntax.FoldCase != 0) {
				return false
			}

			i++
		}

		return next(i)
	case syntax.OpCharClass:
		return i < len(b.in) && inClass(re.Rune, b.in[i]) && next(i+1)
	case syntax.OpAnyChar:
		return i < len(b.in) && next(i+1)
	case syntax.OpAnyCharNotNL:
		return i < len(b.in) && b.in[i] != '\n' && next(i+1)
	case syntax.OpCapture:
		return b.match(re.Sub[0], i, next)
	case syntax.OpConcat:
		return b.concat(re.Sub, i, next)
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if b.match(sub, i, next) {
				return true
			}
		}

		return false
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
		lo, hi := repeatBounds(re)
		return b.repeat(re.Sub[0], lo, hi, 0, i, next)
	default:
		return b.empty(re.Op, i) && next(i)
	}
}

// concat matches parts in order at in[i:].
func (b *backtracker) concat(parts []*syntax.Regexp, i int, next func(int) bool) bool {
	if len(parts) == 0 {
		return next(i)
	}

	return b.match(parts[0], i, func(j int) bool { return b.concat(parts[1:], j, next) })
}

// repeat matches sub between lo and hi more times (hi < 0: unbounded), count done so far, greedy first.
func (b *backtracker) repeat(sub *syntax.Regexp, lo, hi, count, i int, next func(int) bool) bool {
	if hi < 0 || count < hi {
		if b.match(sub, i, func(j int) bool { return j != i && b.repeat(sub, lo, hi, count+1, j, next) }) {
			return true
		}
	}

	return count >= lo && next(i)
}

// empty reports whether the empty-width op holds at in[i].
func (b *backtracker) empty(op syntax.Op, i int) bool {
	switch op {
	case syntax.OpEmptyMatch:
		return true
	case syntax.OpBeginText:
		return i == 0
	case syntax.OpEndText:
		return i == len(b.in)
	case syntax.OpBeginLine:
		return i == 0 || b.in[i-1] == '\n'
	case syntax.OpEndLine:
		return i == len(b.in) || b.in[i] == '\n'
	case syntax.OpWordBoundary:
		return b.word(i-1) != b.word(i)
	case syntax.OpNoWordBoundary:
		return b.word(i-1) == b.word(i)
	default:
		return false
	}
}

// word reports whether in[i] is a word character.
func (b *backtracker) word(i int) bool {
	return i >= 0 && i < len(b.in) && syntax.IsWordChar(b.in[i])
}

// repeatBounds returns the bounds of the repetition re; hi < 0 is unbounded.
func repeatBounds(re *syntax.Regexp) (lo, hi int) {
	switch re.Op {
	case syntax.OpStar:
		return 0, -1
	case syntax.OpPlus:
		return 1, -1
	case syntax.OpQuest:
		return 0, 1
	default:
		return re.Min, re.Max
	}
}

// variableRepeats counts the repetitions of a variable count in re: the degree of the polynomial a safe pattern may take.
func variableRepeats(re *syntax.Regexp) int {
	n := 0

	switch re.Op {
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
		if lo, hi := repeatBounds(re); lo != hi {
			n++
		}
	default:
	}

	for _, sub := range re.Sub {
		n += variableRepeats(sub)
	}

	return n
}

// treeChoices counts, independently of [regexcache.Safe], the ways the tree re may try one attempt: the product of the branch
// counts of its alternations and the product of the repeat counts of its bounded variable repetitions.
func treeChoices(re *syntax.Regexp) (branches, repeats int) {
	branches, repeats = 1, 1

	switch re.Op {
	case syntax.OpAlternate:
		branches = len(re.Sub)
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
		if lo, hi := repeatBounds(re); hi >= 0 && lo != hi {
			repeats = hi - lo + 1
		}
	default:
	}

	for _, sub := range re.Sub {
		b, r := treeChoices(sub)
		branches, repeats = min(branches*b, math.MaxInt32), min(repeats*r, math.MaxInt32)
	}

	return branches, repeats
}

// sameRune reports whether r and c are the same rune, or case folds of each other under fold.
func sameRune(r, c rune, fold bool) bool {
	if r == c {
		return true
	}

	if !fold {
		return false
	}

	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f == c {
			return true
		}
	}

	return false
}

// inClass reports whether c is in the rune ranges class.
func inClass(class []rune, c rune) bool {
	for i := 0; i+1 < len(class); i += 2 {
		if class[i] <= c && c <= class[i+1] {
			return true
		}
	}

	return false
}

// unfold parses pattern, which [regexcache.Safe] accepts, into the tree a backtracking engine runs: groups and alternations as
// written, each atom (a character, an escape or a class with its quantifier) parsed alone by the Go parser under the flags in
// force. It reports false for a shape it does not split, such as a quantifier after a group, which Safe rejects.
func unfold(pattern string) (*syntax.Regexp, bool) {
	u := unfolder{src: unquote(pattern)}

	re, ok := u.alternation(nil)
	if !ok || u.i != len(u.src) {
		return nil, false
	}

	return re, true
}

// unquote rewrites every \Q...\E of pattern outside a class as escaped text, which both the Go parser and an engine read alike:
// \Q(|)\E is \(\|\), and 0\Q\E*0 is 0*0.
func unquote(pattern string) string {
	var b strings.Builder

	for i := 0; i < len(pattern); {
		j := i

		switch {
		case strings.HasPrefix(pattern[i:], `\Q`):
			j = regexcache.SkipEscape(pattern, i, true)
			b.WriteString(regexp.QuoteMeta(strings.TrimSuffix(pattern[i+len(`\Q`):j], `\E`)))

			i = j

			continue
		case pattern[i] == '\\':
			j = regexcache.SkipEscape(pattern, i, true)
		case pattern[i] == '[':
			j = regexcache.SkipClass(pattern, i)
		default:
			j++
		}

		b.WriteString(pattern[i:j])
		i = j
	}

	return b.String()
}

// unfolder is the state of [unfold]: the pattern and the index read up to.
type unfolder struct {
	src string
	i   int
}

// alternation reads branches up to the ) that closes the enclosing group or the end, under flags (the bodies of the flag groups
// in force, outermost first).
func (u *unfolder) alternation(flags []string) (*syntax.Regexp, bool) {
	flags = slices.Clip(flags)
	branches := [][]*syntax.Regexp{nil}

	for u.i < len(u.src) && u.src[u.i] != ')' {
		last := len(branches) - 1

		switch u.src[u.i] {
		case '|':
			branches = append(branches, nil)
			u.i++
		case '(':
			sub, inline, ok := u.group(flags)
			if !ok {
				return nil, false
			}

			if sub == nil { // a flag group without a colon: in force up to the end of the enclosing group, across its branches
				flags = append(flags, inline)
				continue
			}

			branches[last] = append(branches[last], sub)
		default:
			atom, ok := u.atom(flags)
			if !ok {
				return nil, false
			}

			branches[last] = append(branches[last], atom)
		}
	}

	alt := make([]*syntax.Regexp, 0, len(branches))
	for _, parts := range branches {
		switch len(parts) {
		case 0:
			alt = append(alt, &syntax.Regexp{Op: syntax.OpEmptyMatch})
		case 1:
			alt = append(alt, parts[0])
		default:
			alt = append(alt, &syntax.Regexp{Op: syntax.OpConcat, Sub: parts})
		}
	}

	if len(alt) == 1 {
		return alt[0], true
	}

	return &syntax.Regexp{Op: syntax.OpAlternate, Sub: alt}, true
}

// group reads the group at u.src[u.i] (an opening parenthesis): its tree, or a nil tree and the body of a flag group without a
// colon such as (?i), which sets flags for the rest of the enclosing group.
func (u *unfolder) group(flags []string) (sub *syntax.Regexp, inline string, ok bool) {
	u.i++ // (

	capture := true

	if strings.HasPrefix(u.src[u.i:], "?") {
		rest := u.src[u.i+1:]

		switch {
		case strings.HasPrefix(rest, "P<"), strings.HasPrefix(rest, "<"):
			end := strings.IndexByte(rest, '>')
			if end < 0 {
				return nil, "", false
			}

			u.i += 1 + end + 1
		default:
			capture = false

			end := strings.IndexAny(rest, ":)")
			if end < 0 {
				return nil, "", false
			}

			body := rest[:end]

			u.i += 1 + end + 1
			if rest[end] == ')' {
				return nil, body, true
			}

			flags = append(slices.Clip(flags), body)
		}
	}

	sub, ok = u.alternation(flags)
	if !ok || u.i >= len(u.src) {
		return nil, "", false
	}

	u.i++ // )

	if capture {
		sub = &syntax.Regexp{Op: syntax.OpCapture, Sub: []*syntax.Regexp{sub}}
	}

	return sub, "", true
}

// atom reads one character, escape or class with its quantifier and parses it under flags.
func (u *unfolder) atom(flags []string) (*syntax.Regexp, bool) {
	start := u.i

	switch u.src[u.i] {
	case '\\':
		u.i = regexcache.SkipEscape(u.src, u.i, true)
	case '[':
		u.i = regexcache.SkipClass(u.src, u.i)
	default:
		_, size := utf8.DecodeRuneInString(u.src[u.i:])
		u.i += size
	}

	// The quantifiers, each maybe lazy; the parser accepts a second one only where the first leaves nothing to repeat (0{0}*).
	for u.i < len(u.src) {
		if strings.IndexByte("*+?", u.src[u.i]) >= 0 {
			u.i++
		} else if n := regexcache.RepeatLen(u.src[u.i:]); n > 0 {
			u.i += n
		} else {
			break
		}

		if u.i < len(u.src) && u.src[u.i] == '?' {
			u.i++ // lazy
		}
	}

	var prefix strings.Builder
	for _, f := range flags {
		prefix.WriteString("(?" + f + ")")
	}

	re, err := syntax.Parse(prefix.String()+u.src[start:u.i], syntax.Perl)

	return re, err == nil
}

// worstSteps returns the most steps the model takes on pattern over inputs of length n built from a few runes, those of the
// pattern among them, and false when an input passes the budget.
func worstSteps(re *syntax.Regexp, pattern string, n int) (int, bool) {
	alphabet := []rune("ab0 !\n")
	for _, r := range pattern {
		if len(alphabet) < 16 && !slices.Contains(alphabet, r) {
			alphabet = append(alphabet, r)
		}
	}

	var inputs []string

	for _, r := range alphabet {
		run := strings.Repeat(string(r), n)
		inputs = append(inputs, run, run[:len(run)-len(string(r))]+"!")
	}

	inputs = append(inputs, strings.Repeat("ab", n/2))

	worst := 0

	for _, in := range inputs {
		b := backtracker{in: []rune(in)}

		steps, ok := b.run(re)
		if !ok {
			return steps, false
		}

		worst = max(worst, steps)
	}

	return worst, true
}

// polynomialGrowth returns, for a pattern that parses, how much the model's worst steps grow from modelShort to modelLong, and the
// most a polynomial of the pattern's degree (its variable repetitions plus the choice of start) grows by, times 4 to spare for
// terms such as (n-3)^3 that grow faster than n^3 at these lengths.
func polynomialGrowth(t *testing.T, pattern string) (ratio, bound float64) {
	t.Helper()

	re, ok := unfold(pattern)
	require.True(t, ok, "%q does not unfold", pattern)

	return growth(t, re, pattern)
}

// growth is [polynomialGrowth] of the tree re of pattern.
func growth(t *testing.T, re *syntax.Regexp, pattern string) (ratio, bound float64) {
	t.Helper()

	short, ok := worstSteps(re, pattern, modelShort)
	require.True(t, ok, "%q passes the step budget at length %d", pattern, modelShort)

	long, ok := worstSteps(re, pattern, modelLong)
	require.True(t, ok, "%q passes the step budget at length %d", pattern, modelLong)

	return float64(long) / float64(max(short, 1)), float64(int(1) << (variableRepeats(re) + 3))
}

// TestBacktrackerModel_DetectsExponential pins that the model tells an exponential pattern from a polynomial one.
func TestBacktrackerModel_DetectsExponential(t *testing.T) {
	t.Parallel()

	// A repeated group does not unfold (Safe rejects it); the parser keeps (a|aa)+ as written.
	exponential, err := syntax.Parse(`^(a|aa)+$`, syntax.Perl)
	require.NoError(t, err)

	ratio, bound := growth(t, exponential, `^(a|aa)+$`)
	require.Greater(t, ratio, bound)

	ratio, bound = polynomialGrowth(t, `^a*b$`)
	require.LessOrEqual(t, ratio, bound)
}

// TestBacktrackerModel_UnfoldsAlternations pins that the model runs every branch of an alternation the Go parser folds: a chain
// of two-way groups ((?:.|a)(?:.|a)…$) is exponential in the model, as in PCRE, while the folded tree (.{12}$) is not.
func TestBacktrackerModel_UnfoldsAlternations(t *testing.T) {
	t.Parallel()

	for _, group := range []string{`(?:.|a)`, `(a|a)`, `(?:\w|\d)`, `(?i:a|A)`} {
		pattern := strings.Repeat(group, 12) + "$"

		ratio, bound := polynomialGrowth(t, pattern)
		require.Greater(t, ratio, bound, pattern)

		folded, err := syntax.Parse(pattern, syntax.Perl)
		require.NoError(t, err)

		ratio, bound = growth(t, folded, pattern)
		require.LessOrEqual(t, ratio, bound, pattern)
	}
}

// TestUnfold pins the tree unfold builds for each shape it splits.
func TestUnfold(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ pattern, want string }{
		{`abc`, `abc`},
		{`a|a`, `a|a`},
		{`(?:a|a)b`, `(?:a|a)b`},
		{`(a|b)`, `(a|b)`},
		{`(?P<n>a|b)`, `(a|b)`},
		{`(?i)a|b`, `(?i:A|B)`},
		{`x(?i:a)b`, `x(?i:A)b`},
		{`a*?b{2,3}`, `a*?b{2,3}`},
		{`\Q(|)\E|[|]`, `\(\|\)|\|`},
		{`a||`, `a|(?:)|(?:)`},
		{`\x41|\x{42}+`, `A|B+`},
	} {
		re, ok := unfold(tc.pattern)
		require.True(t, ok, tc.pattern)
		require.Equal(t, tc.want, re.String(), tc.pattern)
	}

	for _, pattern := range []string{`\PC`, `x|\pL+`, `\p{Greek}?`, `(?)a`, `0{00}*`, `0\Q\E*0`, `\Qa|b`} {
		_, ok := unfold(pattern)
		require.True(t, ok, pattern)
	}

	for _, pattern := range []string{`(a)*`, `(?i`, `(?P<n`} {
		_, ok := unfold(pattern)
		require.False(t, ok, pattern)
	}
}

// FuzzSafe_PolynomialSteps checks that a pattern Safe accepts runs in the backtracking model in steps that grow polynomially
// with the input, never exponentially.
func FuzzSafe_PolynomialSteps(f *testing.F) {
	for _, seed := range []string{
		`abc`, `^(foo|bar)$`, `(?i)^hello.*`, `^[a-z]+@example\.(com|org)$`, `^https?://[^/]+/.*$`, `^[a-z]*[0-9]+$`,
		`^\d+\.\d+$`, `^[a-z]+-[0-9]{2,4}.*x$`, `^A.*e$`, `a{0,100}b*`, `a*b*`, `a?b?c?d?`, `[ab]{0,30}c[ab]{0,30}`,
		`.*\n.*`, `^a.*!`, `a*!`, `^(?:a|ab)b*$`, `\bab+\b`, `(?:.|a)(?:.|a)(?:.|a)(?:.|a)(?:.|a)$`,
		`(a|a)(a|a)(a|a)(a|a)(a|a)(a|a)(a|a)(a|a)$`, `^(?:a|ab|abc)(?:a|ab|abc)(?:a|ab|abc)$`, `(?:a|)(?:a|)(?:a|)aaa$`,
		`(?i)(?:jan|feb|mar)-(?:mon|tue|wed)$`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, pattern string) {
		if len(pattern) > 64 || regexcache.Safe(pattern) != nil {
			t.Skip()
		}

		re, ok := unfold(pattern)
		require.True(t, ok, "%q does not unfold", pattern)

		branches, repeats := treeChoices(re)
		require.LessOrEqual(t, branches*repeats, regexcache.MaxRepeatProduct, "%q has %d branch and %d repeat choices", pattern,
			branches, repeats)

		// The alternations of a pattern longer than modelShort come into play only on the longer inputs, so they may add their
		// constant factor to the growth on top of the polynomial.
		ratio, bound := growth(t, re, pattern)
		require.LessOrEqual(t, ratio, bound*float64(branches), "%q grows by %.1f from length %d to %d", pattern, ratio, modelShort,
			modelLong)
	})
}
