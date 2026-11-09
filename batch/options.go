// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package batch

//go:generate optgen generate --type=validateOptions --option-type=Option --output=options_gen.go
//go:generate optgen generate --type=runOptions --option-type=RunOption --output=run_options_gen.go

import (
	"context"
	"errors"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Defaults of the request checks and of [Partial].
const (
	// DefaultMaxItems is the default maximum number of items of a batch,
	// the limit the AIP request examples document.
	DefaultMaxItems = 1000
	// DefaultConcurrency is the default number of items [Partial] runs at
	// once: one, in request order.
	DefaultConcurrency = 1
)

// validateOptions configures [CheckSize] and [ValidateRequest].
type validateOptions struct {
	// WithMaxItems bounds the items of a batch, checked before any other work. Zero is unlimited; a negative
	// value keeps the default.
	maxItems int `optgen:"default=DefaultMaxItems" optval:"positive=allow_zero"`
	// WithAllowEmpty accepts a batch without items. By default an empty batch is an INVALID_ARGUMENT error on the
	// repeated field: AIP-231 says so for Batch Get, and the repeated field of every batch method is REQUIRED.
	allowEmpty bool `optval:"param"`
	// WithUniqueKeys makes [ValidateRequest] reject two items naming the same resource ("names", or the "name"
	// of each nested request or of its resource). Off by default: AIP-231 to AIP-235 do not forbid duplicates,
	// and a Batch Get returns a resource once per name. Turn it on for Batch Update and Batch Delete.
	uniqueKeys bool `optval:"param"`
}

// runOptions configures [Partial].
type runOptions struct {
	// WithConcurrency bounds the items Partial runs at once. Zero or a negative value keeps the default of one.
	concurrency int `optgen:"default=DefaultConcurrency" optval:"positive"`
	// WithFatal classifies the item errors that stop the whole batch instead of being reported as failed
	// requests. AIP-233 to AIP-235 forbid reporting an error that a server-side retry could fix in
	// failed_requests. The default matches context cancellation and deadline errors and
	// coreerrs.ErrUnavailable; nil makes every item error a failed request.
	fatal func(error) bool `optgen:"default=DefaultFatal"`
}

// DefaultFatal is the default [WithFatal] classifier: a context error or
// [coreerrs.ErrUnavailable], the transient failures a retry may fix.
func DefaultFatal(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, coreerrs.ErrUnavailable)
}
