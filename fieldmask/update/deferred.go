// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskmerge"
	"github.com/altessa-s/go-aip/internal/maskpath"
)

// DeferredIntent describes the request operation awaiting stored state.
type DeferredIntent uint8

const (
	// DeferredSet compares a supplied protected value.
	DeferredSet DeferredIntent = iota
	// DeferredClear compares an explicitly selected empty protected value.
	DeferredClear
	// DeferredReplace compares a whole collection, element, or atomic message,
	// including protected values in elements removed by the replacement.
	DeferredReplace
)

// DeferredOperation describes a comparison retained by a [DeferredPlan].
// Values are held in the plan's private snapshot, never borrowed from the request.
type DeferredOperation struct {
	// Path is the client's path resolved to declared names, including a covering
	// path or "*". For an implied mask or an unnamed identifier it is Field.
	Path string
	// Field is the protected field or replacement being compared. Collection
	// sub-paths retain their mask wildcard; concrete values are in the snapshot.
	Field string
	// Behavior is the IMMUTABLE or IDENTIFIER behavior requiring comparison.
	Behavior annotations.FieldBehavior
	// Intent distinguishes a supplied value, an explicit clear and a replacement.
	Intent DeferredIntent
}

// DeferredPlan retains the result and pending comparisons of [Prepare], bound
// to the exact resource instance passed to it. Keep it only for that request's
// lifetime; it retains that instance, the policy (including resolver callbacks),
// and one deep copy of the original resource and raw mask, even when Operations
// is empty. Completion may need stored-state expansion; Spec needs unselected
// secret input, and Patch's allow_missing branch needs the entire create input.
// Bound Spec/Patch consumers obtain that input through the boundary's stateless
// OUTPUT_ONLY sanitization; only standalone Complete replays the raw snapshot.
// No copy is made per operation or element.
// Raw and effective paths obey the configured mask limits before any snapshot
// is cloned; stored-dependent expansion and wildcard exclusions stay unresolved
// until completion, which checks the final expansion limits. Deferred descendants share
// the existing bounded traversal.
// Payload storage is bounded by one original resource;
// path limits do not bound its byte size. Apply transport payload limits too.
//
// Complete uses a temporary copy of the snapshot and never retains stored state.
// Operations and Mask return copies. Completion may be repeated with the same
// stored state, but calls and mutations of the bound resource must be serialized.
// The zero value is invalid.
type DeferredPlan struct {
	resource     proto.Message
	original     proto.Message
	raw          *fieldmaskpb.FieldMask
	policy       *options
	mask         fieldmask.FieldMask
	pending      []DeferredOperation
	needsCurrent bool // validation or mask normalization depends on stored state
	resolved     bool // Prepare already had current state
}

// Prepare applies the same checks as [ApplyMask], and retains
// the original resource and raw mask for completion, secret consumption and
// allow_missing creation. [DeferredPlan.Complete] finishes stored-state work.
// After capturing the input, Prepare filters unselected values and clears OUTPUT_ONLY
// input even when no write paths remain. Routing identities and unverified
// protected values are retained under the same policy as non-empty masks.
// An explicit empty mask remains a no-op with no comparisons or secret changes.
// With [WithCurrent] it finishes immediately and retains no pending comparisons.
// On failure it returns no plan and leaves resource unchanged. A stateless
// refusal precedes stored-state comparison, so it can report fewer violations
// than a call supplied with current state; only successful preparation can be
// completed.
// Errors have ApplyMask's shape: a [fieldmask.ValidationError], [BehaviorError],
// or, with [WithStrict], [StrictError].
func Prepare(raw *fieldmaskpb.FieldMask, resource proto.Message, opts ...Option) (*DeferredPlan, error) {
	return prepare(raw, resource, true, opts...)
}

// prepare shares transactional validation while preserving ApplyMask's stateless
// empty-mask behavior. Only Prepare captures and sanitizes empty selections.
func prepare(raw *fieldmaskpb.FieldMask, resource proto.Message, deferred bool, opts ...Option) (*DeferredPlan, error) {
	o := newOptions(opts...)
	if o.strict {
		o.pathValidation = PathValidationEnforce
	}

	p := &DeferredPlan{resource: resource, policy: o, resolved: o.current != nil}

	work, err := cloneMessage(resource)
	if err == nil {
		var pending *DeferredPlan
		if deferred {
			pending = p
		}

		p.mask, err = applyMask(raw, work, o, pending)
	}

	if err == nil {
		err = publishMessage(resource, work)
	}

	if err != nil {
		if o.strict {
			err = strictError(err, o.updateMaskFieldName)
		}

		return nil, err
	}

	// A supplied current resource must never be retained by the result.
	o.current, o.deferredPlan, o.planContext = nil, nil, nil

	return p, nil
}

// Mask returns a copy of the effective mask from the stateless stage. Deferred
// replacements are added only to the mask returned by [DeferredPlan.Complete].
func (p *DeferredPlan) Mask() fieldmask.FieldMask {
	return p.mask.Clone()
}

// Operations returns a copy of the deferred comparisons, including clear intent.
func (p *DeferredPlan) Operations() []DeferredOperation {
	return slices.Clone(p.pending)
}

// Complete finishes the update against stored without changing stored. On
// success its mask and resource match [ApplyMask] on the original request with
// [WithCurrent](stored), including its explicit-empty no-op behavior. If Prepare
// already had current state, Complete keeps that sanitized result unchanged.
// A validation or behavior refusal restores the original
// request, matching a one-stage refusal. A message mismatch or conversion
// failure leaves the bound resource as it was immediately before Complete.
// Changed protected values produce a *[BehaviorError]; [WithStrict] retains its
// usual *[StrictError] wrapping and sentinel identities. Later request mutations
// do not affect completion of a stateless plan. With a state-independent mask
// and no pending comparisons it returns Mask without reading stored, which may
// then be nil. An empty Operations result alone does not imply this fast path:
// collection validation, expansion and retained routing-message sanitization
// may still depend on stored state.
// Otherwise stored must be a valid message
// of the resource's type, distinct from the bound resource.
// Mask defects use ApplyMask's validation error, including limits discovered
// during final expansion; strict mode returns its usual [StrictError].
func (p *DeferredPlan) Complete(stored proto.Message) (mask fieldmask.FieldMask, err error) {
	defer func() {
		if err != nil && p != nil && p.policy != nil && p.policy.strict {
			if _, wrapped := errors.AsType[*StrictError](err); !wrapped {
				err = strictError(err, p.policy.updateMaskFieldName)
			}
		}
	}()

	mask, work, err := p.complete(stored)
	if err != nil {
		if p != nil && p.original != nil && !errors.Is(err, ErrMessageMismatch) {
			if copyErr := publishMessage(p.resource, p.original); copyErr != nil {
				return nil, copyErr
			}
		}

		return nil, err
	}

	if work != nil {
		if err := publishMessage(p.resource, work); err != nil {
			return nil, err
		}
	}

	return mask, nil
}

// complete evaluates the boundary policy for the standalone Complete API.
func (p *DeferredPlan) complete(stored proto.Message) (fieldmask.FieldMask, proto.Message, error) {
	if p == nil || p.resource == nil {
		return nil, nil, ErrMessageMismatch
	}

	if p.resolved {
		return p.Mask(), nil, nil
	}

	if p.needsCurrent && (stored == nil || sameResource(p.resource, stored)) {
		return nil, nil, ErrMessageMismatch
	}

	work, err := cloneMessage(p.original)
	if err != nil {
		return nil, nil, err
	}

	o := *p.policy
	// Only a provably independent mask may avoid reading stored: no protected
	// comparisons (including unnamed identifiers), no covering expansion, and
	// no message collection traversal whose targets or exclusions need storage,
	// and no retained routing message whose sanitization restores stored values.
	// Empty masks and ignored OUTPUT_ONLY selections satisfy this rule too.
	if p.needsCurrent {
		o.current = stored
	} else {
		// The proof above permits stateful filtering with an empty resource;
		// no decision can inspect its values or the caller's stored message.
		o.current = p.original.ProtoReflect().Type().New().Interface()
	}

	mask, err := applyMask(p.raw, work, &o, nil)
	if err != nil && o.strict {
		err = strictError(err, o.updateMaskFieldName)
	}

	return mask, work, err
}

// ContextWithPlan attaches plan for its bound resource. Different resources,
// including different messages of a stream, have separate context keys. Use a
// fresh resource instance and request context for each received stream message;
// do not reuse an instance while its previous context is still in use.
func ContextWithPlan(ctx context.Context, plan *DeferredPlan) context.Context {
	if plan == nil || plan.resource == nil {
		return ctx
	}

	return context.WithValue(ctx, planContextKey{resource: resourceIdentity(plan.resource)}, plan)
}

// PlanFromContext finds the plan for this exact resource instance, never a clone
// or another message of the same type. Retrieval does not consume the plan.
func PlanFromContext(ctx context.Context, resource proto.Message) (*DeferredPlan, bool) {
	if ctx == nil || resource == nil {
		return nil, false
	}

	p, ok := ctx.Value(planContextKey{resource: resourceIdentity(resource)}).(*DeferredPlan)

	return p, ok && sameResource(p.resource, resource)
}

type planContextKey struct{ resource any }

// resourceIdentity uses only comparable identities; protobuf implementations
// without a pointer identity cannot share a context plan accidentally.
func resourceIdentity(resource proto.Message) any {
	v := reflect.ValueOf(resource)
	if v.Kind() == reflect.Pointer {
		return resource
	}

	return nil
}

func sameResource(a, b proto.Message) bool {
	key := resourceIdentity(a)
	return key != nil && key == resourceIdentity(b)
}

func (p *DeferredPlan) capture(raw *fieldmaskpb.FieldMask, resource proto.Message, policy *options) error {
	var explicit []maskPath

	for i, path := range raw.GetPaths() {
		resolved, err := maskpath.ResolvePath(resource.ProtoReflect().Descriptor(), path, policy.strictDynamicPaths)
		if err == nil {
			explicit = append(explicit, maskPath{index: i, raw: resolved, segs: maskpath.Split(resolved)})
		}
	}

	for i := range p.pending {
		op := &p.pending[i]
		op.Path = op.Field

		if _, path := maskIndex(explicit, op.Field); path != "" {
			op.Path = path
		} else if slices.Equal(raw.GetPaths(), []string{maskpath.Wildcard}) {
			op.Path = maskpath.Wildcard
		}
	}

	original, err := cloneMessage(resource)
	if err != nil {
		return err
	}

	p.original = original
	if raw != nil {
		p.raw = proto.CloneOf(raw)
	}

	return nil
}

func (u *updateCheck) deferProtected(
	fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, pairs []msgPair, path string, b annotations.FieldBehavior,
) {
	u.needsCurrent = true

	if u.deferred == nil {
		return
	}

	if nested != nil && u.deferProtectedSelection(fd, nested, pairs, path, b) {
		return
	}

	if len(pairs) == 0 {
		u.deferOperation(DeferredOperation{Path: path, Field: path, Behavior: b, Intent: DeferredClear})
	}

	for _, pair := range pairs {
		intent := DeferredClear
		if pair.req != nil && pair.req.Has(fd) {
			intent = DeferredSet
		}

		u.deferOperation(DeferredOperation{Path: path, Field: path, Behavior: b, Intent: intent})
	}
}

// deferProtectedSelection records clear intent below a field whose behavior
// protects the entire value, such as an IMMUTABLE message selected by a leaf.
func (u *updateCheck) deferProtectedSelection(
	fd protoreflect.FieldDescriptor, nested fieldmask.FieldMask, pairs []msgPair, path string, b annotations.FieldBehavior,
) bool {
	md := maskpath.ValueMessage(fd)
	if md == nil || maskpath.IsDynamicMessage(md) {
		return false
	}

	if !fd.IsList() && !fd.IsMap() {
		u.deferProtectedFields(md, nested, childPairs(pairs, fd), path, b)
		return true
	}

	for _, key := range slices.Sorted(maps.Keys(nested)) {
		selected := elementPairs(fd, key, nested, pairs)
		elemPath := elementPath(path, key)

		if sub := nested[key]; sub != nil {
			u.deferProtectedFields(md, sub, selected, elemPath, b)
		} else {
			u.deferOperation(DeferredOperation{Path: elemPath, Field: elemPath, Behavior: b, Intent: DeferredReplace})
		}
	}

	return true
}

func (u *updateCheck) deferProtectedFields(
	md protoreflect.MessageDescriptor, nested fieldmask.FieldMask, pairs []msgPair, path string, b annotations.FieldBehavior,
) {
	for _, name := range slices.Sorted(maps.Keys(nested)) {
		if fd := md.Fields().ByName(protoreflect.Name(name)); fd != nil {
			u.deferProtected(fd, nested[name], pairs, behavior.JoinPath(path, name), b)
		}
	}
}

func (u *updateCheck) deferReplacement(md protoreflect.MessageDescriptor, path string) {
	u.needsCurrent = true

	if u.deferred == nil {
		return
	}

	seen := map[protoreflect.FullName]bool{}

	var visit func(protoreflect.MessageDescriptor)

	visit = func(md protoreflect.MessageDescriptor) {
		if seen[md.FullName()] {
			return
		}

		seen[md.FullName()] = true
		fields := md.Fields()

		for i := range fields.Len() {
			fd := fields.Get(i)
			b := u.policy.serverManaged(fd)

			switch {
			case b == annotations.FieldBehavior_OUTPUT_ONLY:
			case isImmutableBehavior(b):
				u.deferOperation(DeferredOperation{Path: path, Field: path, Behavior: b, Intent: DeferredReplace})
			default:
				if child := maskpath.ValueMessage(fd); child != nil {
					visit(child)
				}
			}
		}
	}

	visit(md)
}

func (u *updateCheck) deferOperation(op DeferredOperation) {
	if _, exists := u.deferredSet[op]; exists || u.refusal != nil {
		return
	}

	if u.deferredSet == nil {
		u.deferredSet = make(map[DeferredOperation]struct{})
	}

	u.deferredSet[op] = struct{}{}
	u.deferred.pending = append(u.deferred.pending, op)
}

// planFor resolves request-scoped options without allowing a plan to cross
// resource identities. An explicitly supplied unrelated plan is an error.
func (o *options) planFor(resource proto.Message) (*DeferredPlan, error) {
	if p := o.deferredPlan; p != nil {
		if !sameResource(p.resource, resource) {
			return nil, ErrMessageMismatch
		}

		return p, nil
	}

	p, _ := PlanFromContext(o.planContext, resource)

	return p, nil
}

// planDeferred replays the boundary only for refusals. The Spec alone plans
// writes from the captured input after stateless OUTPUT_ONLY sanitization.
func (s *Spec) planDeferred(resource proto.Message, o *options, pending *DeferredPlan) (result Plan, err error) {
	original := originalMaskProvenance(pending.raw)
	origins := original

	defer func() { err = origins.translate(err, s.opts.updateMaskFieldName) }()

	if sameResource(resource, o.current) {
		return Plan{}, ErrMessageMismatch
	}

	request, err := pending.boundaryInput()
	if err != nil {
		return Plan{}, s.completionError(err, pending.raw, pending.policy)
	}

	defer func() {
		if err == nil {
			if copyErr := publishMessage(resource, request); copyErr != nil {
				result, err = Plan{}, copyErr
			}
		}
	}()

	if o.current != nil {
		// Never reuse Complete's cached or state-independent result: this is
		// the stored snapshot against which the boundary must decide now.
		policy := *pending.policy
		policy.current = o.current

		check, copyErr := cloneMessage(request)
		if copyErr != nil {
			return Plan{}, copyErr
		}

		if _, checkErr := applyMask(pending.raw, check, &policy, nil); checkErr != nil {
			checkErr = s.orderImpliedBoundaryRefusal(checkErr, pending, request, o.current)
			return Plan{}, s.completionError(checkErr, pending.raw, pending.policy)
		}
	}

	// The Spec's raw limits apply to what the client sent, even when the
	// boundary drops enough OUTPUT_ONLY entries to put K0 under the limit.
	if checkErr := original.checkLimits(pending.raw, s.opts); checkErr != nil {
		return Plan{}, s.fieldError(checkErr)
	}

	input, raw, sanitizedOrigins, err := pending.specInput(request)
	if err != nil {
		return Plan{}, s.completionError(err, pending.raw, pending.policy)
	}

	request = input
	origins = sanitizedOrigins

	if checkErr := origins.checkLimits(raw, s.opts); checkErr != nil {
		return Plan{}, s.fieldError(checkErr)
	}

	plan, err := s.Plan(input, raw, WithCurrent(o.current))
	if err != nil {
		return Plan{}, err
	}

	if o.current != nil {
		origins = original // the boundary guard is located against the raw mask

		if err := s.checkBoundaryEffect(input, o.current, plan, pending.policy); err != nil {
			return Plan{}, s.completionError(err, pending.raw, pending.policy)
		}
	}

	return plan, nil
}

// checkBoundaryEffect covers direct MergePaths consumers and Patch's sanitized
// merge source, plus every secret operation Save can consume from Plan.Secret.
// Neither check changes the source or selection produced by the Spec.
func (s *Spec) checkBoundaryEffect(input, current proto.Message, plan Plan, boundary *options) error {
	if plan.Empty() {
		return nil
	}

	if err := s.checkBoundaryWrite(input, input, current, plan, boundary); err != nil {
		return err
	}

	policy := *s.opts
	policy.current = current

	source, err := cloneMessage(input)
	if err != nil {
		return err
	}

	if err := clearOutputOnlyKeeping(source, fieldmask.FromPaths(plan.Paths...), &policy); err != nil {
		return err
	}

	if !proto.Equal(source, input) {
		return s.checkBoundaryWrite(source, input, current, plan, boundary)
	}

	return nil
}

func (s *Spec) checkBoundaryWrite(source, input, current proto.Message, plan Plan, boundary *options) error {
	after, err := cloneMessage(current)
	if err != nil {
		return err
	}

	if err = mergePaths(after, source, plan.Paths, s.opts); err != nil {
		return err
	}

	// A named but unset secret is an explicit default value, not a protobuf
	// Clear: setting even an empty string selects its oneof member.
	for _, fd := range s.order {
		name := string(fd.Name())
		switch {
		case slices.Contains(plan.set, name):
			if err = maskmerge.SetValue(after.ProtoReflect(), fd, input.ProtoReflect().Get(fd)); err != nil {
				return conversionError(err)
			}
		case slices.Contains(plan.named, name):
			value := after.ProtoReflect().NewField(fd)
			if fd.Kind() == protoreflect.StringKind && !fd.IsList() && !fd.IsMap() {
				value = protoreflect.ValueOfString("") // Plan.Secret clears even a non-empty proto2 default
			}

			after.ProtoReflect().Set(fd, value)
		}
	}

	u, root, err := newApplyCheck(after, current, boundary)
	if err != nil {
		return err
	}

	u.protectedChanges(nil, root)

	if u.refusal != nil {
		return u.refusal
	}

	if len(u.violations) > 0 {
		return &BehaviorError{Violations: u.violations}
	}

	return nil
}
