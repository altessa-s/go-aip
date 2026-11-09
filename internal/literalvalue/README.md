# literalvalue

```go
import "github.com/altessa-s/go-aip/internal/literalvalue"
```

Internal rules shared by enum declarations and cached filter ASTs. Scalars and reference-free arrays and structs are immutable.
Time and decimal values use their immutable public contracts. Canonical encoding frames each component and ignores custom formatting methods.
Byte literals are encoded here and cloned by the AST layer. Other reference-bearing values must be rejected before encoding.

## Contract

This is an internal implementation package. Its exported helpers are available only to packages permitted by Go internal import rules. Mutable work
state belongs to the caller or an explicitly constructed instance; consumers should use the public parent APIs.

## API

| Symbol      | Purpose                                                                        |
|-------------|--------------------------------------------------------------------------------|
| `Immutable` | Immutable reports whether copying v cannot alias caller-mutable state.         |
| `Canonical` | Canonical encodes an immutable value without invoking user formatting methods. |

## Related packages

- [go-aip](../../README.md)
- [internal/behavior](../behavior/README.md)
