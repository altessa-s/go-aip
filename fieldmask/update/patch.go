// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/etag"
	"github.com/altessa-s/go-aip/fieldmask"

	coreetag "github.com/altessa-s/go-atlas-core/encoding/etag"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// patchOptions configures one [Patch.Run] request.
type patchOptions struct {
	// allowMissing is the AIP-134 allow_missing flag of the request: when the
	// resource does not exist, it is created from the request resource with
	// [Patch.Create] and the update mask is ignored.
	allowMissing bool `optval:"param"`
	// validateOnly is the AIP-163 validate_only flag of the request: every
	// check runs (precondition, mask paths, field behaviors, merge) but
	// neither [Patch.Save] nor [Patch.Create] is called, and the resource
	// the update or the allow_missing create would store is returned.
	validateOnly bool `optval:"param"`
}

// Patch orchestrates a masked read-modify-write (AIP-134). Precondition owns
// revision parsing and comparison (AIP-154); Spec owns path policy and the
// AIP-203 field behaviors, which it applies against the stored resource Read
// returns (see [Spec.Plan] with [WithCurrent]). Save receives the parsed
// revision and must atomically enforce it while storing the merged resource.
type Patch[E any, P proto.Message, V any] struct {
	// Field is the request field holding the resource.
	Field string
	Spec  *Spec
	// Precondition parses and checks the caller's revision contract.
	Precondition Precondition[E, V]
	// Read returns the stored resource; one that does not exist is an error
	// matching [coreerrs.ErrNotFound].
	Read    func(ctx context.Context) (E, error)
	ToProto func(E) P
	// Save converts and stores the merged resource.
	Save func(ctx context.Context, merged P, plan Plan, expected V) (E, error)
	// Create stores the request resource when the request allows a missing
	// resource ([WithAllowMissing]) and Read reports it does not exist. The
	// resource arrives with its OUTPUT_ONLY values cleared; the create-time
	// checks (REQUIRED fields, identifiers) are the caller's. Nil leaves
	// allow_missing unsupported: such a request is [ErrInvalidPatch].
	Create func(ctx context.Context, in P) (E, error)
}

// Run applies an update after checking the precondition, including for empty
// plans: it parses the revision, refuses a malformed or unknown mask path
// before the read (unless allow_missing may create or a bound plan requires
// boundary-first validation), reads the stored resource, checks the revision against
// it, plans the mask against it ([Spec.Plan] with [WithCurrent], so an
// unchanged IMMUTABLE or IDENTIFIER value is ignored and a changed one
// refused with a [coreerrs.FieldError] on Field + "." + its path,
// matching [ErrProtectedField]), clears the OUTPUT_ONLY values of
// the request, merges the planned paths and saves. It returns the stored
// resource or the first error. A [DeferredPlan] attached to ctx with
// [ContextWithPlan] is completed for this exact resource before the merge;
// handlers need no changes after an interceptor switches to [Prepare]. With a
// bound plan, Run behaves as on the original request and mask after the boundary's
// OUTPUT_ONLY sanitization, with additional refusals when the boundary rejects
// that request against the current stored resource or the complete write,
// including Plan.Secret operations, changes a value the boundary protects.
//
// mask keeps its presence (AIP-134): nil (update_mask omitted) updates the
// fields the request populates, a mask with no paths (update_mask sent
// empty) is a no-op that saves nothing and returns the stored resource after
// the precondition check, and "*" replaces the resource.
//
// With [WithAllowMissing](true), a Read error matching [coreerrs.ErrNotFound]
// creates the resource instead (AIP-134 allow_missing): the precondition must
// accept a missing resource ([Precondition.Missing]; [Int64Precondition] and
// [TagPrecondition] refuse an etag sent for one as [coreetag.ErrMismatch], a
// [coreerrs.ErrConflict]), the update mask is ignored entirely, including
// invalid paths, and Create receives a copy of the original request without its
// OUTPUT_ONLY values. A bound [DeferredPlan] supplies that input after the
// boundary's stateless OUTPUT_ONLY clearing, even when normalization removed
// fields outside the update mask. The Spec's OUTPUT_ONLY clearing also applies.
// Without Create, such a request is [ErrInvalidPatch].
//
// With [WithValidateOnly](true) (AIP-163 validate_only), Run performs every
// step above but calls neither Save nor Create: it returns the merged
// resource an update would save, or the resource an allow_missing create
// would receive, so a validation request fails exactly when the live request
// would fail before its write. Values the store assigns on save (a new etag
// or update time, a generated identifier) are not computed: the returned
// resource keeps the stored values, or none for a create. Create is still
// required for an allow_missing request.
//
// Library mutations are private until every check and callback succeeds. On
// error the request and the stored proto returned by ToProto are unchanged.
// Save consumes its merged argument and Plan.Secret; request restoration is
// published only after Save returns successfully. Callbacks remain responsible
// for the atomicity of their own external writes.
func (p Patch[E, P, V]) Run(ctx context.Context, in P, tag string, mask *fieldmaskpb.FieldMask, opts ...PatchOption) (P, error) {
	var zero P

	if !validMessage(in) {
		return zero, ErrMessageMismatch
	}

	work, err := cloneMessage(in)
	if err != nil {
		return zero, err
	}

	request, ok := work.(P)
	if !ok {
		return zero, ErrMessageMismatch
	}

	if pending, ok := PlanFromContext(ctx, in); ok {
		bound := *pending
		bound.resource = request
		ctx = ContextWithPlan(ctx, &bound)
	}

	var prepared proto.Message

	ready := func() error {
		var prepareErr error

		prepared, prepareErr = preparePublication(in, request)

		return prepareErr
	}

	result, err := p.run(ctx, request, tag, mask, ready, opts...)
	if err != nil {
		return zero, err
	}

	commitMessage(in, prepared)

	return result, nil
}

// valid reports whether the update workflow has all required dependencies.
func (p Patch[E, P, V]) valid() bool {
	return !nilcheck.IsNil(p.Precondition) && p.Read != nil && p.ToProto != nil && p.Save != nil && p.Spec != nil
}

// run owns its request and merge destination, even when ToProto returns the
// stored instance. Callback failures cannot publish partial library mutations.
func (p Patch[E, P, V]) run(
	ctx context.Context, in P, tag string, mask *fieldmaskpb.FieldMask, ready func() error, opts ...PatchOption,
) (result P, err error) {
	if pending, ok := PlanFromContext(ctx, in); ok && p.Spec != nil {
		// Spec has already translated its errors; all remaining branches
		// (including allow_missing and callbacks) use client coordinates.
		defer func() { err = originalMaskProvenance(pending.raw).translate(err, p.Spec.opts.updateMaskFieldName) }()
	}

	var zero P
	if !p.valid() {
		return zero, ErrInvalidPatch
	}

	o := newPatchOptions(opts...)
	if o.allowMissing && p.Create == nil {
		return zero, ErrInvalidPatch
	}

	expected, err := p.Precondition.Parse(tag)
	if err != nil {
		return zero, err
	}

	// Bound plans validate their captured mask, with boundary refusals first;
	// preflighting the rewritten mask would change that precedence.
	_, deferred := PlanFromContext(ctx, in)
	if !o.allowMissing && !deferred {
		if err = p.Spec.checkPaths(mask.GetPaths()); err != nil {
			return zero, err
		}
	}

	cur, err := p.Read(ctx)
	if o.allowMissing && errors.Is(err, coreerrs.ErrNotFound) {
		return p.create(ctx, in, expected, o.validateOnly, ready)
	}

	if err != nil {
		return zero, err
	}

	if o.allowMissing && !deferred {
		if err = p.Spec.checkPaths(mask.GetPaths()); err != nil {
			return zero, err
		}
	}

	if err = p.Precondition.Check(cur, expected); err != nil {
		return zero, err
	}

	snapshot, err := cloneCurrent(in, p.ToProto(cur))
	if err != nil {
		return zero, err
	}

	merged, ok := snapshot.(P)
	if !ok {
		return zero, ErrMessageMismatch
	}

	plan, err := p.Spec.Plan(in, mask, WithCurrent(merged), WithPlanContext(ctx))
	if changed, ok := errors.AsType[*changedError](err); ok {
		return zero, &coreerrs.FieldError{Field: p.Field + "." + changed.path, Reason: "cannot be changed", Err: err}
	}

	if err != nil {
		return zero, err
	}

	if plan.Empty() {
		if err = ready(); err != nil {
			return zero, err
		}

		return merged, nil
	}

	plan.captureSecrets(in)

	if err = p.Spec.mergePlan(merged, in, plan); err != nil {
		return zero, err
	}

	if err = ready(); err != nil {
		return zero, err
	}

	if o.validateOnly {
		return merged, nil
	}

	out, err := p.Save(ctx, merged, plan, expected)
	if err != nil {
		return zero, err
	}

	return p.ToProto(out), nil
}

// mergePlan applies the selected input to the private stored-resource copy.
func (s *Spec) mergePlan(merged, in proto.Message, plan Plan) error {
	src, err := cloneMessage(in)
	if err != nil {
		return err
	}

	policy := *s.opts
	policy.current = merged

	if err = clearOutputOnlyKeeping(src, fieldmask.FromPaths(plan.Paths...), &policy); err != nil {
		return err
	}

	if err = mergePaths(merged, src, plan.Paths, s.opts); err != nil {
		return err
	}

	return nil
}

// captureSecrets retains input-only strings for the save callback.
func (p *Plan) captureSecrets(in proto.Message) {
	p.secrets = make(map[string]string, len(p.set))
	for _, name := range p.set {
		fd := in.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name))
		if fd != nil && fd.Kind() == protoreflect.StringKind && !fd.IsList() && !fd.IsMap() {
			p.secrets[name] = in.ProtoReflect().Get(fd).String()
		}
	}
}

// create is the allow_missing branch of [Patch.Run]: it checks the
// precondition against the missing resource and creates in without its
// OUTPUT_ONLY values; validateOnly returns that resource without creating
// it.
func (p Patch[E, P, V]) create(ctx context.Context, in P, expected V, validateOnly bool, ready func() error) (P, error) {
	var zero P
	if err := p.Precondition.Missing(expected); err != nil {
		return zero, err
	}

	var input proto.Message = in
	if pending, ok := PlanFromContext(ctx, in); ok {
		var err error

		input, err = pending.boundaryInput()
		if err != nil {
			return zero, err
		}
	}

	cloned, err := cloneMessage(input)
	if err != nil {
		return zero, err
	}

	src, ok := cloned.(P)
	if !ok {
		return zero, ErrMessageMismatch
	}

	if err = clearOutputOnly(src, p.Spec.opts); err != nil {
		return zero, err
	}

	if err = ready(); err != nil {
		return zero, err
	}

	if validateOnly {
		return src, nil
	}

	out, err := p.Create(ctx, src)
	if err != nil {
		return zero, err
	}

	return p.ToProto(out), nil
}

// ErrInvalidPatch reports missing workflow dependencies.
var ErrInvalidPatch = errors.New("fieldmask: missing patch dependency")

// Precondition separates revision syntax and concurrency policy from
// mutation. V may be an opaque token, composite revision, or optional
// precondition. Check runs even for an empty update plan; Missing runs
// instead of it when an allow_missing update finds no resource. Save must
// enforce the same token atomically.
type Precondition[E, V any] interface {
	// Parse decodes the revision the request sends.
	Parse(tag string) (V, error)
	// Check compares the revision with the stored resource.
	Check(entity E, expected V) error
	// Missing checks the revision against a resource that does not exist:
	// nil lets an allow_missing update create it.
	Missing(expected V) error
}

// Int64Precondition is the AIP-154 integer-revision preset, built on
// [etag.Precondition.ParseRevision]: the etag of a stored version is
// [etag.FromRevision], W/"<version>". Its revision is optional: a request
// without an etag carries no precondition (a nil revision) unless Required
// is set, and Save then stores without a version check. A present etag must
// name the stored version, else the update is [coreetag.ErrMismatch], a
// [coreerrs.ErrConflict] (ABORTED for the API).
type Int64Precondition[E any] struct {
	// Field is the API path of the etag field errors name, such as
	// "book.etag".
	Field string
	// Singleton accepts W/"0", the etag of a singleton resource that has
	// not been saved yet.
	Singleton bool
	// Required refuses a request without an etag with a [coreerrs.FieldError]
	// on Field ([etag.ReasonRequired]) instead of updating unconditionally.
	Required bool
	// Version returns the stored version of an entity.
	Version func(E) int64
}

// Parse decodes the revision of the etag and attributes errors to Field (a
// [coreerrs.FieldError] with [etag.ReasonRequired] or [etag.ReasonInvalid]);
// an empty tag is a nil revision unless the etag is Required.
func (p Int64Precondition[E]) Parse(tag string) (*int64, error) {
	if p.Version == nil {
		return nil, ErrInvalidPatch
	}

	pre := etag.NewPrecondition(etag.WithField(p.Field), etag.WithRequired(p.Required), etag.WithSingleton(p.Singleton))

	v, ok, err := pre.ParseRevision(tag)
	if err != nil || !ok {
		return nil, err
	}

	return &v, nil
}

// Check rejects a stale version with [coreetag.ErrMismatch]; a nil revision
// accepts any.
func (p Int64Precondition[E]) Check(entity E, expected *int64) error {
	if p.Version == nil {
		return ErrInvalidPatch
	}

	if expected != nil && p.Version(entity) != *expected {
		return coreetag.ErrMismatch
	}

	return nil
}

// Missing accepts a missing resource only without a revision: an etag names
// a version the missing resource does not have, [coreetag.ErrMismatch].
func (p Int64Precondition[E]) Missing(expected *int64) error {
	if expected != nil {
		return coreetag.ErrMismatch
	}

	return nil
}

// TagPrecondition is the AIP-154 preset for opaque etags, such as content
// tags from [etag.FromMessage] or tags stored with the resource. Its
// revision is the parsed [coreetag.Tag], the zero Tag when the request sends no
// etag (no precondition unless the Rule requires one). Rule configures the
// field name, whether the etag is required and the [etag.Comparison]
// ([etag.CompareExact] by default); Etag returns the current tag of a stored
// entity. A sent etag that does not match it is [coreetag.ErrMismatch], a
// [coreerrs.ErrConflict]. Save receives the parsed tag to enforce
// atomically.
type TagPrecondition[E any] struct {
	Rule etag.Precondition
	Etag func(E) coreetag.Tag
}

// Parse decodes the etag with [etag.Precondition.Parse].
func (p TagPrecondition[E]) Parse(tag string) (coreetag.Tag, error) {
	if p.Etag == nil {
		return coreetag.Tag{}, ErrInvalidPatch
	}

	return p.Rule.Parse(tag)
}

// Check matches the parsed etag against the current tag of entity with
// [etag.Precondition.Match]; the zero Tag accepts any.
func (p TagPrecondition[E]) Check(entity E, expected coreetag.Tag) error {
	if p.Etag == nil {
		return ErrInvalidPatch
	}

	return p.Rule.Match(expected, p.Etag(entity))
}

// Missing accepts a missing resource only without an etag: the missing
// resource has no current tag, so a sent one is [coreetag.ErrMismatch].
func (p TagPrecondition[E]) Missing(expected coreetag.Tag) error {
	return p.Rule.Match(expected, coreetag.Tag{})
}
