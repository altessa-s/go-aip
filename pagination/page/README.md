# page

```go
import "github.com/altessa-s/go-aip/pagination/page"
```

`page.Page[T]` carries one AIP-158 list page: its items and the token for the next page. The package has no dependencies, so domain
models can expose it while listing engines return it.

## Contract

`Page[T]` contains Items in list order and NextPageToken. An empty next token marks the last page. The package has no dependencies; domain APIs and
all listing engines can share this result shape without importing a database driver.

## Fields

| Field           | Meaning                                                |
|-----------------|--------------------------------------------------------|
| `Items`         | Items in the current page                              |
| `NextPageToken` | Opaque token for the next page; empty on the last page |

## Usage

```go
type Page[T any] = page.Page[T]
```

The in-memory, SQL (`backend/sqlbase`) and MongoDB (`backend/mongo`) engines of [`listing`](../../listing) return this type.

## API

| Symbol | Purpose                                      |
|--------|----------------------------------------------|
| `Page` | Page is one page of a list result (AIP-158). |

## Related packages

- [go-aip](../../README.md)
- [pagination/pagetoken](../pagetoken/README.md)
- [pagination guide](../../docs/pagination.md)
