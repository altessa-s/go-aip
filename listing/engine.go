// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing

import (
	"context"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/pagination/page"
)

// Engine serves pages with an explicit engine-specific source or scope S.
// Memory uses []T; database engines use their typed equality scopes.
type Engine[T, S any] interface {
	List(context.Context, string, Request, S) (page.Page[T], error)
}

// Lister serves pages after the source and subject have been bound.
type Lister[T any] interface {
	List(context.Context, Request) (page.Page[T], error)
}

type boundEngine[T, S any] struct {
	engine  Engine[T, S]
	subject string
	scope   S
}

func (b boundEngine[T, S]) List(ctx context.Context, req Request) (page.Page[T], error) {
	return b.engine.List(ctx, b.subject, req, b.scope)
}

// Bind supplies a source and subject, producing the same port for every engine.
// The caller owns scope and must not mutate it while requests are running.
func Bind[T, S any](engine Engine[T, S], subject string, scope S) Lister[T] {
	return boundEngine[T, S]{engine: engine, subject: subject, scope: scope}
}

// ErrorMapper maps a query failure at the engine boundary. filtered reports whether
// a predicate was sent. Implementations should preserve err in the returned chain.
type ErrorMapper func(ctx context.Context, err error, filtered bool) error

// FilterParser parses a caller-owned filter AST. Implementations must be concurrency safe.
type FilterParser interface {
	Parse(context.Context, string) (filter.Node, error)
}

// OrderParser parses order syntax. Implementations must be concurrency safe.
type OrderParser interface {
	Parse(context.Context, string) (orderby.Spec, error)
}

// ContractID returns the application-versioned contract used to bind page tokens.
// Change it when changing parser, evaluation, or storage ordering semantics.
func (s *Schema) ContractID() string { return s.opts.contractID }
