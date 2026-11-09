# decimal128

```go
import "github.com/altessa-s/go-aip/decimal128"
```

IEEE 754-2008 decimal128 representability and canonical form for [shopspring](https://github.com/shopspring/decimal) `decimal.Decimal`, in
pure Go and without a database driver: whether a decimal128 (the BSON Decimal128 of MongoDB) holds a decimal exactly, and the form it stores
it in. Nothing is rounded; a decimal either fits exactly or is refused. [filter](../filter) bounds decimal literals with it,
[filter/evaluator](../filter/evaluator) sizes its decimal work by its range, and the [MongoDB backend](../backend/mongo)'s decimal codec
stores exactly the decimals `Fits` accepts.

## Key symbols

| Symbol                          | Purpose                                                                                                          |
|---------------------------------|------------------------------------------------------------------------------------------------------------------|
| `MaxDigits`, `MinExp`, `MaxExp` | The decimal128 range: 34 significant digits, exponent -6176..6111                                                |
| `MaxCoefficientBits`            | The largest coefficient `Fits` and `Canonical` inspect (4096 bits); a larger one does not fit                    |
| `MaxCoefficientDigits`          | The most decimal digits a coefficient of `MaxCoefficientBits` bits has (1234)                                    |
| `CoefficientWithinLimit`        | Whether a coefficient has at most `MaxCoefficientBits` bits, in time independent of its size                     |
| `SmallCoefficient`              | Whether a coefficient is below 2^113: a cheap bit-width check for pricing, not representability (use `Fits`)     |
| `Normalize`                     | Significant digits and exponent once trailing zeros fold into the exponent; zero is `(0, 0)`                     |
| `Representable`                 | Whether a normalized decimal is in the decimal128 range                                                          |
| `Fits`                          | Whether a decimal128 holds the decimal exactly: coefficient within `MaxCoefficientBits` and `Representable`      |
| `Canonical`                     | The exact form a decimal128 stores and reads back (the MongoDB driver's conversion); zero is `decimal.Zero`      |

## Range and canonical form

Trailing zeros of the coefficient fold into the exponent, and a short coefficient may be padded to bring a large exponent down, so the range
is judged on the normalized form: `1200E3` is two digits with exponent 5, and `1E6144` fits as `1000…0E6111` (34 digits). `Fits` judges the
stored value, not the decimal the caller keeps: a zero fits with any exponent, and a fitting coefficient may carry trailing zeros.
`Canonical` returns the same value with at most 34 digits and an exponent in range, changed only as far as needed, so comparing, rescaling
or printing it takes bounded work whatever exponent the input had.

## Usage

```go
if !decimal128.Fits(d) {
    return errOutOfRange
}
c, _ := decimal128.Canonical(d) // bounded coefficient and exponent, same value
```

## Untrusted input

`Fits` and `Canonical` refuse a coefficient of more than `MaxCoefficientBits` bits before copying or formatting it, so they take bounded
time on any `decimal.Decimal`, even one whose trailing zeros would fold into range. `Normalize` formats the coefficient; bound an untrusted
coefficient before calling it with `CoefficientWithinLimit`, which compares it with the bound at its own exponent without copying it
(`filter` caps decimal literals, and the MongoDB codec and value encoding the decimals they encode, this way).
