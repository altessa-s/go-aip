# typename

```go
import "github.com/altessa-s/go-aip/internal/typename"
```

Spells the type of a value in an error message, bounded. [filter](../../filter) and [filter/evaluator](../../filter/evaluator) name the
type of an operand or a data value with it; the value may come from the data, and the name of a struct type constructed at run time
(`reflect.StructOf`) lists all its fields and their tags, so the name is cut to 128 bytes. `backend/mongo` keeps its own copy for its
`size()` operand check, as a nested module may not import it.

## Contract

This is an internal implementation package. Its exported helpers are available only to packages permitted by Go internal import rules.

## API

| Symbol | Purpose                                                                                                     |
|--------|-------------------------------------------------------------------------------------------------------------|
| `Of`   | The name of the type of a value, as `%T` formats it, cut to 128 bytes followed by `...`; `<nil>` for nil.   |

## Related packages

- [filter](../../filter/README.md)
- [filter/evaluator](../../filter/evaluator/README.md)
