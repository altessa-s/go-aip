// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package softdelete

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/filter"
)

// Wildcard is the AIP-165 filter that matches every resource of the parent,
// accepted under [WithWildcard].
const Wildcard = "*"

// PurgeRequest is the common shape of an AIP-165 Purge request.
type PurgeRequest struct {
	// Parent is the collection the purge applies to; empty for a top-level
	// collection.
	Parent string
	// Filter selects the resources to purge (AIP-160). It is required.
	Filter string
	// Force deletes the matching resources; false is a dry run that only
	// counts them and returns a sample.
	Force bool
}

// PurgeResponse is the common shape of an AIP-165 Purge response.
type PurgeResponse struct {
	// PurgeCount is the number of resources deleted, or that a dry run would
	// delete. It is the store's count, an estimate if the store says so,
	// clamped to math.MaxInt32.
	PurgeCount int32
	// PurgeSample holds up to [WithSampleSize] names of the resources a dry
	// run would delete, in the store's order; empty when Force is set.
	PurgeSample []string
}

// ProtoPurgeRequest is the getter set every generated Purge request message
// shares (AIP-165).
type ProtoPurgeRequest interface {
	GetFilter() string
	GetForce() bool
}

// parentGetter is a Purge request of a child collection.
type parentGetter interface {
	GetParent() string
}

// RequestFromProto converts a generated Purge request message to a
// [PurgeRequest]: filter, force and, when the message has a GetParent method,
// parent.
func RequestFromProto(msg ProtoPurgeRequest) PurgeRequest {
	req := PurgeRequest{Filter: msg.GetFilter(), Force: msg.GetForce()}
	if p, ok := msg.(parentGetter); ok {
		req.Parent = p.GetParent()
	}

	return req
}

// Parser parses an AIP-160 filter into a caller-owned AST, a
// *filter/parser.Parser say. It must be safe for concurrent use.
type Parser interface {
	Parse(ctx context.Context, expr string) (filter.Node, error)
}

// Store performs the deletion a [Purger] decided. The filter it receives is
// validated against the purger's policy; nil matches every resource of the
// parent (the wildcard). Implementations must be safe for concurrent use.
type Store interface {
	// Preview counts the resources of parent that f matches and returns the
	// names of up to sampleSize of them (none when sampleSize is zero).
	Preview(ctx context.Context, parent string, f filter.Node, sampleSize int) (count int64, sample []string, err error)
	// Purge deletes the resources of parent that f matches and returns their
	// number.
	Purge(ctx context.Context, parent string, f filter.Node) (count int64, err error)
}

// Purger serves AIP-165 Purge requests: it validates the request, parses and
// validates the filter, and delegates the count, the sample and the
// deletion to a [Store]. It is immutable and safe for concurrent use.
type Purger struct {
	policy *filter.Policy
	parser Parser
	store  Store
	opts   *purgerOptions
}

// NewPurger returns a purger whose filters parser parses and policy
// validates (the allowed fields, depth and operation limits), and whose
// store deletes. All three are required ([ErrInvalidPurger]).
func NewPurger(policy *filter.Policy, parser Parser, store Store, opts ...PurgerOption) (*Purger, error) {
	if policy == nil || nilcheck.IsNil(parser) || nilcheck.IsNil(store) {
		return nil, fmt.Errorf("%w: policy, parser and store are required", ErrInvalidPurger)
	}

	return &Purger{policy: policy, parser: parser, store: store, opts: newPurgerOptions(opts...)}, nil
}

// Purge serves req. It checks, in order: parent (required under
// [WithParentRequired]), then filter — required and non-blank, within
// [WithMaxFilterLength], "*" only under [WithWildcard], parsed and valid
// under the policy — and fails with an InvalidArgument
// core/errors.FieldError on the first invalid field. Without Force it is a
// dry run: the store counts the matches and samples their names. With Force
// the matches are counted first when [WithMaxPurgeCount] is set, more than
// the limit is a field error on filter matching [ErrTooManyResources], and
// the store then deletes them. Store errors are returned as they are.
func (p *Purger) Purge(ctx context.Context, req PurgeRequest) (PurgeResponse, error) {
	node, err := p.compile(ctx, req)
	if err != nil {
		return PurgeResponse{}, err
	}

	if req.Force {
		return p.force(ctx, req.Parent, node)
	}

	return p.dryRun(ctx, req.Parent, node)
}

// dryRun counts and samples the resources node matches.
func (p *Purger) dryRun(ctx context.Context, parent string, node filter.Node) (PurgeResponse, error) {
	count, sample, err := p.store.Preview(ctx, parent, node, p.opts.sampleSize)
	if err != nil {
		return PurgeResponse{}, err
	}

	n, err := purgeCount(count)
	if err != nil {
		return PurgeResponse{}, err
	}

	if len(sample) > p.opts.sampleSize {
		sample = sample[:p.opts.sampleSize]
	}

	return PurgeResponse{PurgeCount: n, PurgeSample: sample}, nil
}

// force deletes the resources node matches, after counting them against
// the limit.
func (p *Purger) force(ctx context.Context, parent string, node filter.Node) (PurgeResponse, error) {
	if p.opts.maxPurgeCount > 0 {
		count, _, err := p.store.Preview(ctx, parent, node, 0)
		if err != nil {
			return PurgeResponse{}, err
		}

		if count > p.opts.maxPurgeCount {
			return PurgeResponse{}, fieldError(FieldFilter,
				fmt.Sprintf("matches %d resources, more than the limit of %d; narrow the filter", count, p.opts.maxPurgeCount),
				ErrTooManyResources)
		}
	}

	count, err := p.store.Purge(ctx, parent, node)
	if err != nil {
		return PurgeResponse{}, err
	}

	n, err := purgeCount(count)
	if err != nil {
		return PurgeResponse{}, err
	}

	return PurgeResponse{PurgeCount: n}, nil
}

// compile validates the request fields and returns the validated filter;
// nil is the wildcard.
func (p *Purger) compile(ctx context.Context, req PurgeRequest) (filter.Node, error) {
	if p.opts.parentRequired && strings.TrimSpace(req.Parent) == "" {
		return nil, fieldError(FieldParent, "is required", nil)
	}

	expr := strings.TrimSpace(req.Filter)
	if expr == "" {
		return nil, fieldError(FieldFilter, "is required", nil)
	}

	if p.opts.maxFilterLength > 0 && len(req.Filter) > p.opts.maxFilterLength {
		return nil, fieldError(FieldFilter,
			fmt.Sprintf("is %d bytes long, more than the limit of %d", len(req.Filter), p.opts.maxFilterLength), ErrFilterTooLong)
	}

	if expr == Wildcard {
		if p.opts.wildcard {
			return nil, nil //nolint:nilnil // a nil filter is the wildcard
		}

		return nil, fieldError(FieldFilter, `the wildcard "*" is not supported; select the resources to purge`, nil)
	}

	node, err := p.parser.Parse(ctx, expr)
	if err != nil {
		return nil, filterError(err)
	}

	if err = filter.Validate(node, p.policy); err != nil {
		return nil, filterError(err)
	}

	return node, nil
}

// filterError reports an invalid filter as a field error on filter. A parser
// or validator echoes literal operands in its diagnostics, which may carry
// personal data, so only a generic reason reaches the client; err stays in
// the chain.
func filterError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	return fieldError(FieldFilter, "is not a valid filter expression", err)
}

// purgeCount converts a store count to the int32 purgeCount of AIP-165.
func purgeCount(count int64) (int32, error) {
	if count < 0 {
		return 0, fmt.Errorf("%w: negative count %d", ErrInvalidStoreResult, count)
	}

	return int32(min(count, math.MaxInt32)), nil //nolint:gosec // clamped to the int32 range
}
