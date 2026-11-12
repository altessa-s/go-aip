# regexanchor

```go
import "github.com/altessa-s/go-aip/filter/regexanchor"
```

Rewrites the end-of-text anchors of an RE2 pattern so PCRE (MongoDB, MariaDB) and ICU (MySQL 8) read them as RE2 does. Used by the MongoDB and
MariaDB backends for `matches()` (the MongoDB translator and the evaluator validation that mirrors it); public so translators in other modules (every
backend is its own module, and so is a custom backend) can use it. Stateless and safe for concurrent use.

## Key functions

| Function                                    | Description                                                                                   |
|---------------------------------------------|-----------------------------------------------------------------------------------------------|
| `EndOfText(pattern string) (string, error)` | Rewrites each `$` RE2 reads as end of text to `\z`; fails with `ErrInvalidRegex` otherwise    |
| `ErrInvalidRegex`                           | The error of a pattern that cannot be anchored; filter exports it as `filter.ErrInvalidRegex` |

## Why

Outside multi-line mode RE2's `$` matches only at the very end; PCRE's and ICU's also match before a final newline, so `"abc\n"` matches `abc$` there
but not in CEL. `\z` is the absolute end in all three engines. A `$` in multi-line mode, an escaped `\$`, a `$` in a character class and a `$` inside
`\Q…\E` are left alone, and every rewrite is checked against RE2's own parse of the original pattern.

## Related packages

- [filter](../README.md)
- [filter/internal/regexcache](../internal/regexcache/README.md)
- [filter guide](../../docs/filter.md)
