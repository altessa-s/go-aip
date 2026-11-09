// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package extract

import (
	"context"
	"slices"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"
)

// UpdateFunc locates the update_mask, the resource sub-message, and
// (optionally) a writeback closure on req. The writeback closure is invoked
// by the caller after [github.com/altessa-s/go-aip/fieldmask/update.Apply] cleans OUTPUT_ONLY entries;
// return nil from the writeback slot to skip writeback. Return ok=false to
// fall through to a silent passthrough.
//
// The function operates on [proto.Message] and has no gRPC dependency — it
// is the transport-agnostic shape consumed by
// transport/grpc/interceptors/fieldmask but reusable from any caller (HTTP
// handlers, CLI tools, batch jobs).
type UpdateFunc func(ctx context.Context, req proto.Message) (
	mask *fieldmaskpb.FieldMask,
	resource proto.Message,
	writeback func(*fieldmaskpb.FieldMask),
	ok bool,
)

// ReadFunc locates the read_mask on req. Return ok=false to fall
// through to a silent passthrough.
type ReadFunc func(ctx context.Context, req proto.Message) (
	mask *fieldmaskpb.FieldMask,
	ok bool,
)

// NewUpdateFunc builds an [UpdateFunc] from typed getters. The
// returned function:
//
//   - type-asserts the request to ReqT; returns ok=false on type mismatch;
//   - calls getMask(req); returns ok=false when the mask is nil;
//   - calls getResource(req); returns ok=false when the resource is the
//     typed-nil interface;
//   - if setMask is non-nil, wraps it in a writeback closure that bakes in
//     the request pointer so the caller can write the cleaned mask back
//     without re-type-asserting.
//
// Use this builder for requests that do not match the canonical AIP-134
// shape — for example when the mask lives inside a sibling Options
// sub-message:
//
//	extract.NewUpdateFunc(
//	    func(r *pb.UpdateBucketRequest) *fieldmaskpb.FieldMask { return r.GetOptions().GetUpdateMask() },
//	    func(r *pb.UpdateBucketRequest) *pb.Bucket             { return r.GetBucket() },
//	    func(r *pb.UpdateBucketRequest, m *fieldmaskpb.FieldMask) { r.Options.UpdateMask = m },
//	)
func NewUpdateFunc[ReqT, ResT proto.Message](
	getMask func(ReqT) *fieldmaskpb.FieldMask,
	getResource func(ReqT) ResT,
	setMask func(ReqT, *fieldmaskpb.FieldMask),
) UpdateFunc {
	return func(_ context.Context, raw proto.Message) (*fieldmaskpb.FieldMask, proto.Message, func(*fieldmaskpb.FieldMask), bool) {
		req, ok := raw.(ReqT)
		if !ok {
			return nil, nil, nil, false
		}

		if getMask == nil || getResource == nil {
			return nil, nil, nil, false
		}

		mask := getMask(req)
		if mask == nil {
			return nil, nil, nil, false
		}

		res := getResource(req)
		if nilcheck.IsNil(res) {
			return nil, nil, nil, false
		}

		var writeback func(*fieldmaskpb.FieldMask)
		if setMask != nil {
			writeback = func(m *fieldmaskpb.FieldMask) {
				setMask(req, m)
			}
		}

		return mask, res, writeback, true
	}
}

// NewReadFunc builds a [ReadFunc] from a typed getter. Returns
// ok=false on request-type mismatch or when getMask returns nil.
//
//	extract.NewReadFunc(
//	    func(r *pb.GetBucketRequest) *fieldmaskpb.FieldMask { return r.GetOptions().GetReadMask() },
//	)
func NewReadFunc[ReqT proto.Message](
	getMask func(ReqT) *fieldmaskpb.FieldMask,
) ReadFunc {
	return func(_ context.Context, raw proto.Message) (*fieldmaskpb.FieldMask, bool) {
		req, ok := raw.(ReqT)
		if !ok {
			return nil, false
		}

		if getMask == nil {
			return nil, false
		}

		mask := getMask(req)
		if mask == nil {
			return nil, false
		}

		return mask, true
	}
}

// ChainRead returns a [ReadFunc] that invokes each fn in
// order and returns the first result that signals ok=true. ok=false from
// every fn yields ok=false, which the gRPC interceptor treats as a silent
// passthrough.
//
// Nil entries are skipped. ChainRead called with no functions, or
// with only nil entries, returns a no-op extractor that always reports
// ok=false — callers can rely on the result being non-nil and safe to call.
//
// Use the chain to combine modern transports (gRPC metadata per AIP-157)
// with legacy fallbacks (deprecated request-message read_mask per AIP-161)
// without forcing the caller to pick one source.
func ChainRead(fns ...ReadFunc) ReadFunc {
	compact := slices.DeleteFunc(slices.Clone(fns), func(f ReadFunc) bool { return f == nil })
	if len(compact) == 0 {
		return func(context.Context, proto.Message) (*fieldmaskpb.FieldMask, bool) {
			return nil, false
		}
	}

	return func(ctx context.Context, req proto.Message) (*fieldmaskpb.FieldMask, bool) {
		for _, fn := range compact {
			if mask, ok := fn(ctx, req); ok {
				return mask, true
			}
		}

		return nil, false
	}
}

// ChainUpdate mirrors [ChainRead] for
// [UpdateFunc]. Exported for symmetry and per-service composition;
// the gRPC interceptor's built-in update path is a single extractor because
// AIP-134 requires update_mask on the request message and does not sanction
// a side-channel transport.
//
// As with [ChainRead], a call with no functions or only nil
// entries returns a no-op extractor that always reports ok=false.
func ChainUpdate(fns ...UpdateFunc) UpdateFunc {
	compact := slices.DeleteFunc(slices.Clone(fns), func(f UpdateFunc) bool { return f == nil })
	if len(compact) == 0 {
		return func(context.Context, proto.Message) (*fieldmaskpb.FieldMask, proto.Message, func(*fieldmaskpb.FieldMask), bool) {
			return nil, nil, nil, false
		}
	}

	return func(ctx context.Context, req proto.Message) (*fieldmaskpb.FieldMask, proto.Message, func(*fieldmaskpb.FieldMask), bool) {
		for _, fn := range compact {
			if mask, resource, writeback, ok := fn(ctx, req); ok {
				return mask, resource, writeback, true
			}
		}

		return nil, nil, nil, false
	}
}

// DefaultUpdate wraps [UpdateMask] and [SetUpdateMask] in
// the [UpdateFunc] shape. Used by the gRPC interceptor as the
// bottom-of-stack fallback when no per-method override or global default is
// configured. opts are forwarded to both helpers, so [WithMaskField] /
// [WithResourceField] continue to work via the default extractor.
func DefaultUpdate(opts ...Option) UpdateFunc {
	return func(_ context.Context, req proto.Message) (*fieldmaskpb.FieldMask, proto.Message, func(*fieldmaskpb.FieldMask), bool) {
		mask, resource, ok := UpdateMask(req, opts...)
		if !ok {
			return nil, nil, nil, false
		}

		// Invariant: UpdateMask just resolved the mask field on req with
		// the same opts, so SetUpdateMask cannot return ErrFieldNotSettable
		// here. The discard is intentional and safe by construction.
		writeback := func(m *fieldmaskpb.FieldMask) {
			_ = SetUpdateMask(req, m, opts...)
		}

		return mask, resource, writeback, true
	}
}

// DefaultRead wraps [ReadMask] in the [ReadFunc]
// shape. Used by the gRPC interceptor as the bottom-of-stack fallback.
func DefaultRead(opts ...Option) ReadFunc {
	return func(_ context.Context, req proto.Message) (*fieldmaskpb.FieldMask, bool) {
		return ReadMask(req, opts...)
	}
}
