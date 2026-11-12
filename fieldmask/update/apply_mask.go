// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"errors"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/maskpath"
	"github.com/altessa-s/go-aip/internal/maskwalk"
)

// ApplyMask applies the raw update_mask of an update request (AIP-134) to msg
// and returns its effective write mask. Every raw path is checked before paths
// covered by another path are collapsed. Use it instead of converting the raw
// mask with [fieldmask.FromProtoFieldMask] before [Apply].
//
// Without [WithCurrent], IMMUTABLE and IDENTIFIER values are retained for the
// caller to compare with the stored resource; their paths are excluded from the
// effective mask. Use [Prepare] instead for two-stage updates: its [DeferredPlan]
// retains clear intent and replacement values and completes with stored state,
// directly or through [Spec.Plan] / [Patch.Run]. ApplyMask returns only its mask.
//
// A nil raw mask implies populated leaves; empty mutable messages contribute
// no path. A provided mask with no paths is a no-op that leaves msg untouched; "*" alone is a full
// replacement. A mask
// whose paths are all dropped is interim without stored state: completion may
// restore deferred writes, so always complete a [Prepare] plan. OUTPUT_ONLY input is
// cleared. Never store msg itself as the resource after this filtering step.
//
// With current state, equal IMMUTABLE and IDENTIFIER values are ignored and
// changed values, including clears through explicit or covering paths and "*",
// are field behavior violations. Singular messages containing protected fields
// are expanded to their other fields. A whole collection or atomic value with
// protected descendants cannot be safely represented as a replacement without
// current state: its entire covering path is deferred, and its request value is
// retained. With current state those descendants are compared before replacement,
// including removed elements; lists pair by index and maps by key.
// Stored-dependent expansion, collection validation and wildcard exclusions
// remain unresolved without current state. Prepare retains their intent;
// Complete enforces final expansion limits. An interim expansion that cannot
// fit the limits contributes no paths until completion.
//
// The effective mask uses declared field names and excludes OUTPUT_ONLY and
// protected paths. A private filter mask keeps routing identifiers and, without
// current state, unverified protected input. Other unselected values are cleared.
// INPUT_ONLY fields remain ordinary input fields here. [Apply] describes the
// remaining behavior and policy rules.
//
// Raw path limits apply in every [PathValidationMode], before grammar, schema
// and behavior checks. [PathValidationEnforce] rejects invalid paths;
// [PathValidationWarn] reports and drops them; [PathValidationDisabled] drops
// them. Structural restrictions (list indexes, atomic descent and strict dynamic
// paths) are enforced in every mode. "*" must be the only raw path.
//
// A nil or invalid msg, or a current message of another type, is
// [ErrMessageMismatch]. Path errors are *[fieldmask.ValidationError], behavior
// errors *[BehaviorError]. [WithStrict] enforces every path regardless of the
// path-validation option and wraps refusals as *[StrictError], matching
// [ErrInvalidUpdateMask]. On error msg is untouched.
func ApplyMask(raw *fieldmaskpb.FieldMask, msg proto.Message, opts ...Option) (fieldmask.FieldMask, error) {
	p, err := Prepare(raw, msg, opts...)
	if err != nil {
		return nil, err
	}

	return p.Mask(), nil
}

// applyMask is [ApplyMask] under o.
func applyMask(raw *fieldmaskpb.FieldMask, msg proto.Message, o *options, pending *DeferredPlan) (fieldmask.FieldMask, error) {
	if !validMessage(msg) {
		return nil, ErrMessageMismatch
	}

	u, root, err := newApplyCheck(msg, o.current, o)
	if err != nil {
		return nil, err
	}

	u.deferred = pending

	paths := raw.GetPaths()
	if raw == nil {
		u.orderMode = referenceImplied
		paths = u.impliedSelection(root).ToPaths()
		u.checkImplied(msg.ProtoReflect().Descriptor(), root)
	}

	if raw != nil && len(paths) == 0 {
		if pending != nil {
			if err = pending.capture(raw, msg, o); err != nil {
				return nil, err
			}
		}

		return fieldmask.FieldMask{}, nil // a provided empty update_mask is a no-op (AIP-134).
	}

	if err = o.validateMaskLimits(paths); err != nil {
		return nil, err // limits hold in every PathValidationMode
	}

	paths, err = o.checkGrammar(paths)
	if err != nil {
		return nil, err
	}

	md := msg.ProtoReflect().Descriptor()

	var (
		work    fieldmask.FieldMask
		covered []fieldmask.FieldMask
	)

	switch {
	case len(paths) == 0:
		work = fieldmask.FieldMask{} // no writes; implied protected input still needs its comparison
	case len(paths) == 1 && paths[0] == maskpath.Wildcard:
		work = u.replaceAll(md, root)
	default:
		work, covered, err = o.resolveRawPaths(paths, md)
		if err != nil {
			return nil, err
		}

		u.checkCovering(work, covered, md, root)
	}

	if err = u.maskRefusal(work, md); err != nil {
		return nil, err
	}

	violations, err := u.orderedViolations(md)
	if err != nil {
		return nil, err
	}

	if len(violations) > 0 {
		return nil, &BehaviorError{Violations: violations}
	}

	if pending != nil {
		pending.needsCurrent = u.needsCurrent || (len(work) > 0 && retainedIdentityNeedsCurrent(msg, o))
		if err = pending.capture(raw, msg, o); err != nil {
			return nil, err
		}
	}

	if len(work) == 0 {
		return fieldmask.FieldMask{}, clearOutputOnly(msg, o) // no writes, but output-only input is still cleared
	}

	effective := work.Clone()
	dropPolicyIdentifiers(effective, md, o)

	preserveIdentifierFields(work, msg, o)

	if !u.verify {
		preserveUnverified(work, msg.ProtoReflect(), o)
	}

	maskwalk.Filter(work, msg, false)

	if err := clearOutputOnlyKeeping(msg, work, o); err != nil {
		return nil, err
	}

	return effective, nil
}

// checkGrammar returns paths without the ones the grammar rejects, after
// handling each rejection by the path validation mode ([options.pathFailure]):
// an empty or malformed path, and "*" in a mask of several paths.
func (o *options) checkGrammar(paths []string) ([]string, error) {
	kept := make([]string, 0, len(paths))

	for _, p := range paths {
		var failure *fieldmask.ValidationError

		if err := maskpath.CheckSyntax(p); err != nil {
			failure, _ = errors.AsType[*fieldmask.ValidationError](validationError(err))
		} else if p == maskpath.Wildcard && len(paths) > 1 {
			failure = &fieldmask.ValidationError{Path: p, Reason: maskpath.ReasonWildcardNotAlone, Cause: fieldmask.ErrWildcardNotAlone}
		}

		if failure == nil {
			kept = append(kept, p)
		} else if err := o.pathFailure(failure); err != nil {
			return nil, err
		}
	}

	return kept, nil
}

// resolveRawPaths returns the mask of paths, well-formed raw paths of md
// other than "*", spelled by declared names, and the single-path masks of the
// paths it covers ([coverPaths]). A path the schema rejects is handled by
// the path validation mode ([options.pathFailure]) and dropped when let
// through; an index into a repeated field, a path the strict dynamic paths
// forbid and a sub-path into an atomic message are refused in every mode.
func (o *options) resolveRawPaths(paths []string, md protoreflect.MessageDescriptor) (fieldmask.FieldMask, []fieldmask.FieldMask, error) {
	resolved := make([][]string, 0, len(paths))

	for _, p := range paths {
		name, err := maskpath.ResolvePath(md, p, o.strictDynamicPaths)
		if err == nil {
			resolved = append(resolved, maskpath.Split(name))
			continue
		}

		failure, ok := errors.AsType[*fieldmask.ValidationError](validationError(err))
		if !ok {
			return nil, nil, err
		}

		if o.pathValidation == PathValidationEnforce {
			return nil, nil, failure
		}

		if err := maskpath.RejectStructural(md, p, o.strictDynamicPaths); err != nil {
			return nil, nil, validationError(err)
		}

		if err := o.rejectAtomicDescent(md, p); err != nil {
			return nil, nil, err
		}

		if err := o.pathFailure(failure); err != nil {
			return nil, nil, err
		}
	}

	work, covered := coverPaths(resolved)
	for _, m := range append([]fieldmask.FieldMask{work}, covered...) {
		if err := o.checkAtomicMask(m, md, ""); err != nil {
			return nil, nil, err
		}
	}

	return work, covered, nil
}

// pathFailure handles the rejected path failure by the path validation
// mode: [PathValidationEnforce] returns it, [PathValidationWarn] reports it
// to the reporter, if any, and returns nil, and [PathValidationDisabled]
// returns nil.
func (o *options) pathFailure(failure *fieldmask.ValidationError) error {
	switch o.pathValidation {
	case PathValidationEnforce:
		return failure
	case PathValidationWarn:
		if o.pathReporter != nil {
			o.pathReporter(failure)
		}
	case PathValidationDisabled:
	}

	return nil
}

// dropPolicyIdentifiers is [DropIdentifiers] on a mask of md under policy.
func dropPolicyIdentifiers(msk fieldmask.FieldMask, md protoreflect.MessageDescriptor, policy *options) {
	fields := md.Fields()
	for _, name := range policy.identityFields {
		if fd := fields.ByName(protoreflect.Name(name)); fd != nil && isPolicyIdentity(fd, policy) {
			delete(msk, name)
		}
	}

	dropIdentifiers(msk, md, policy)
}

// strictError returns err, a refusal of an update mask, as the *StrictError
// of [ValidateStrict]: the violations of a *BehaviorError, the path of a
// *fieldmask.ValidationError (the mask field for a mask-level one), and the
// mask field for any other error, which is kept as the cause.
func strictError(err error, maskField string) error {
	if be, ok := errors.AsType[*BehaviorError](err); ok {
		return &StrictError{Violations: be.Violations}
	}

	if ve, ok := errors.AsType[*fieldmask.ValidationError](err); ok {
		path := ve.Path
		if path == "" { // a limit of the whole mask
			path = maskField
		}

		return &StrictError{Cause: err, Violations: []FieldViolation{{Path: path, Reason: ve.Reason}}}
	}

	return &StrictError{Cause: err, Violations: []FieldViolation{{Path: maskField, Reason: err.Error()}}}
}

// retainedIdentityNeedsCurrent accounts for normalization outside the write
// mask: sanitizing a retained policy-only routing message restores its protected
// descendants from storage. Scalar routing identifiers need no such work.
func retainedIdentityNeedsCurrent(msg proto.Message, policy *options) bool {
	m := msg.ProtoReflect()
	for _, name := range policy.identityFields {
		fd := m.Descriptor().Fields().ByName(protoreflect.Name(name))
		if fd == nil || !m.Has(fd) || !isPolicyIdentity(fd, policy) {
			continue
		}

		if md := maskpath.ValueMessage(fd); md != nil && holdsImmutable(md, map[protoreflect.FullName]bool{}, policy) {
			return true
		}
	}

	return false
}
