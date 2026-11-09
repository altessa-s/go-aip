# regexcache

```go
import "github.com/altessa-s/go-aip/filter/internal/regexcache"
```

Provides isolated bounded caches of compiled RE2 patterns.

## Contract

This is an internal implementation package. Its exported helpers are available only to packages permitted by Go internal import rules. Mutable work
state belongs to the caller or an explicitly constructed instance; consumers should use the public parent APIs.

## Named values

| Constant              | Meaning                                                                                                                |
|-----------------------|------------------------------------------------------------------------------------------------------------------------|
| `MaxSize`             | 256: the number of compiled patterns a cache keeps, as `filter.DefaultRegexCacheSize`                                  |
| `MaxUnboundedRepeats` | MaxUnboundedRepeats is the number of unbounded repetitions (*, +, {n,}) a safe pattern may hold.                       |
| `MaxRepeats`          | MaxRepeats is the number of repetitions of a variable count (*, +, ?, {n,m} with n < m, {n,}) a safe pattern may hold. |
| `MaxRepeatCount`      | MaxRepeatCount is the largest bound of a counted repetition ({n}, {n,m}, {n,}) in a safe pattern.                      |
| `MaxRepeatProduct`    | 1000: the largest product of the choices of a safe pattern: alternation branches and bounded repeat counts.            |

## API

| Symbol    | Purpose                                                                                                                                                                                                                                                  |
|-----------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Cache`   | Cache owns a bounded working set and its counters. A zero size disables caching.                                                                                                                                                                         |
| `New`     | New constructs an isolated cache. Zero disables caching.                                                                                                                                                                                                 |
| `Pattern` | Pattern is a compiled pattern and, once asked for, the verdict of `Safe` on it.                                                                                                                                                                          |
| `Safe`    | Safe reports why a backtracking engine (MongoDB PCRE, PostgreSQL ~, MariaDB REGEXP, MySQL ICU) may take more than polynomial time of a bounded degree, or a large constant factor, to run pattern; nil when it is safe. See [Safe subset](#safe-subset). |

| API                               | Purpose                                                                             |
|-----------------------------------|-------------------------------------------------------------------------------------|
| `New(size)`                       | Construct a cache; zero disables retention, a negative size is an error             |
| `Cache.Compile`                   | Compile or retrieve a pattern as RE2; a pattern that does not compile is not cached |
| `Cache.Lookup`                    | `Compile` plus the pattern's `Safe` verdict, cached with it                         |
| `Cache.Stats`, `Cache.ResetStats` | Inspect and reset instance counters                                                 |
| `Safe`                            | Apply the default backtracking-safe subset                                          |

## Safe subset

`Safe` decides whether a pattern may reach a backtracking engine (MongoDB `$regex`, MariaDB `REGEXP`, PostgreSQL `~`, MySQL
ICU) under `WithSafeRegex`. The patterns are sent to those engines verbatim, so `Safe` judges what they read, not only what RE2
reads. It parses the pattern with `regexp/syntax.Parse` (the Perl syntax, no `Simplify`) and also scans the source, because
the parser already folds `(?:a|a)*` and `(?:.|[^!])*` into a single repeated character class while a backtracking engine still
tries both branches at every step (time doubling with each input character).

A pattern is safe when all of these hold:

| Rule                                                                                                                                                                             | Why                                                                                                                                                                                                    | Rejected example                                                                    |
|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------|
| A repetition (`*`, `+`, `?`, `{n}`, `{n,m}`, `{n,}`) applies only to one character, a class or the dot                                                                           | A repeated group, alternation or repetition is the exponential shape                                                                                                                                   | `(a\|aa)+`, `(a+)+`, `(ab)*`                                                        |
| No quantifier follows `)`, whatever the group reads as                                                                                                                           | The parser folds overlapping alternations the engine still backtracks over                                                                                                                             | `^(?:a\|a)*b`, `^(?:.\|[^!])*!`, `(?:a\|a){100}`                                    |
| At most `MaxUnboundedRepeats` (2) unbounded and `MaxRepeats` (4) variable repetitions                                                                                            | Bounds the degree of the polynomial                                                                                                                                                                    | `x+y+z+`, `a?b?c?d?e?`                                                              |
| No counted bound above `MaxRepeatCount` (100)                                                                                                                                    | Bounds a single repetition                                                                                                                                                                             | `a{1,101}`, `a{101,}`                                                               |
| The branches of every alternation, counted in the source, and the choices of the bounded variable repetitions (`m-n+1`, 2 for `?`) multiply to at most `MaxRepeatProduct` (1000) | Bounds the constant: the ways one attempt can be tried; the parser folds `(a\|a)` and `(?:.\|a)` into one character, but an engine tries both branches, so a chain of k two-way groups takes 2^k tries | `[ab]{0,100}[ab]{0,100}[ab]{0,100}[ac]$`, `(?:.\|a)`×25`$`, `^(?:a\|ab\|abc)`×16`$` |
| No two unbounded repetitions over shared characters in a row, with only parts that can match empty between, looking into groups and alternation branches                         | They split a run between them in every way                                                                                                                                                             | `.*.*`, `\d+\.?\d*`, `\w+\b\w+`, `x(?:a*\|b)a*`                                     |
| Every literal `{` is escaped (`\{`)                                                                                                                                              | Go reads `a{,5}` and `a{ 1,3}` as literals; PCRE2 10.43+ reads them as repetitions                                                                                                                     | `a{,5}`, `(a\|aa){,30}`, `x{`                                                       |
| No octal escape `\1`..`\7`                                                                                                                                                       | Go reads `\12` as a character; PCRE reads it as a back-reference with 12 groups                                                                                                                        | `\12`                                                                               |

Patterns that stay accepted include anchored literals and prefixes or suffixes (`^abc`, `abc$`, `^A.*e$`), simple classes
(`^[A-Z][a-z]+$`, `^\d{4}-\d{2}-\d{2}$`), unrepeated alternations (`^(foo|bar)$`, `^[a-z]+@example\.(com|org)$`), URLs
(`^https?://[^/]+/.*$`) and escaped or quoted metacharacters (`\{\}`, `\Q(a|a)*\E`, `[(|)*{]`). Write a repeated
alternation of single characters as a class (`[ab]*`, not `(?:a|b)*`), and a run of alternatives as one class (`[.a]{25}`, not
`(?:.|a)` 25 times).

The guarantee is polynomial time of a bounded degree with a bounded constant, not linear time: `.*x.*y` stays accepted and takes
quadratic time per start on a failing row. `safe_model_test.go` runs a backtracking model (greedy, depth first, every attempt
failing) on the tree the source spells, with every alternation unfolded, and fuzzes that every accepted pattern grows polynomially
in its steps, never exponentially, and that its branches and bounded repeat counts multiply to at most `MaxRepeatProduct`.

## Related packages

- [filter](../../README.md)
- [filter/regexanchor](../../regexanchor/README.md)
- [filter guide](../../../docs/filter.md)
