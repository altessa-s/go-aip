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
	"github.com/altessa-s/go-aip/fieldmask/internal/maskpath"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskwalk"
)

// ApplyMask applies the update_mask raw of an update request (AIP-134), exactly
// as the client sent it, to the request resource msg under the field
// behaviors of AIP-203, and returns the effective mask to write back to the
// request. Pass the stored resource with [WithCurrent]: only then can an
// unchanged IMMUTABLE or IDENTIFIER value be told from a change.
//
// Use it on the raw mask of a request in place of [fieldmask.FromProtoFieldMask]
// followed by [Apply]: a [fieldmask.FieldMask] is a set of paths none of which
// covers another, so it cannot hold a path that another path of the mask
// covers ("profile.id" with "profile"); ApplyMask reads every path as sent.
// [ValidateStrict] checks without applying, and [Spec.Plan] plans a
// read-modify-write of the stored resource instead of filtering msg.
//
// The presence of raw decides the mode (AIP-134):
//   - nil (update_mask omitted): the implied mask, [FromSetFields] of msg
//     under the options, is applied and returned;
//   - no paths (update_mask sent empty): a no-op that returns a non-nil
//     empty mask and leaves msg untouched; with [WithEmptyMaskImplied] it is
//     treated as omitted. A mask that names paths is never implied, even
//     when every one of them is dropped below: it is then a no-op too;
//   - "*" alone: a full replacement of every field but the OUTPUT_ONLY ones,
//     as with Apply.
//
// The paths are checked in this order, all before msg is touched:
//  1. The limits of the options ([WithMaxPaths], [WithMaxPathLength],
//     [WithMaxPathDepth]) on the raw paths, duplicates included, in every
//     [PathValidationMode].
//  2. The grammar of each raw path: an empty or malformed path, and "*"
//     combined with any other path (another "*" included, matching
//     [fieldmask.ErrWildcardNotAlone]). Under [WithPathValidation],
//     [PathValidationEnforce] (the default) refuses the first, while
//     [PathValidationWarn] reports each one to [WithPathValidationReporter]
//     and drops it, and [PathValidationDisabled] drops it silently.
//  3. The schema and the field behaviors of each raw path on its own, in any
//     spelling of its fields, a path another one covers included: such a path
//     is checked as if sent alone ("profile.id" under "profile" names an
//     IDENTIFIER), though only the covering path is applied. A path the
//     schema rejects follows the mode as in step 2; an index into a repeated
//     field, a sub-path into an atomic message and, with
//     [WithStrictDynamicPaths], a path below a google.protobuf.Struct key are
//     refused in every mode.
//
// Past the checks msg is mutated exactly as Apply mutates it: fields outside
// the mask are cleared, the set IDENTIFIER fields of the masked branches are
// kept, a message holding a server-managed field named whole is replaced
// through its other fields, a collection or map entry named whole is replaced
// by new values (their IMMUTABLE and IDENTIFIER values kept as sent, their
// OUTPUT_ONLY ones cleared) and every OUTPUT_ONLY value left is cleared. The
// field behaviors, the mask policy of the options, the refusal of a path
// that switches a oneof away from a protected member and the treatment of a
// collection are those of Apply.
//
// As with Apply, an omitted mask or "*" whose request sets an IMMUTABLE value
// is refused without [WithCurrent], whatever the value: the stored one is
// unknown, so a change could not be told from an unchanged value (AIP-203).
// Pass the stored resource, or name the paths to update.
//
// The returned mask is the mask the update applied, ready for
// [fieldmask.FieldMask.ToProtoFieldMask] and the request: spelled by declared
// names, with "*" and every message named whole that holds a server-managed
// field expanded so that no path covers an OUTPUT_ONLY, IMMUTABLE or
// IDENTIFIER field, without the OUTPUT_ONLY paths, the unchanged IMMUTABLE
// and IDENTIFIER paths and the IDENTIFIER entries Apply adds to address the
// resource (as [DropIdentifiers] removes them). INPUT_ONLY fields are
// ordinary fields here and stay in it. An empty result means the update
// changes nothing; msg is then left as sent and must not be stored as the
// resource.
//
// A nil or invalid msg, or a [WithCurrent] message of another type, is
// [ErrMessageMismatch]. A rejected path is a *[fieldmask.ValidationError]
// (matching [fieldmask.ErrInvalidPath], and the limit or wildcard sentinel
// that applies) and field behavior violations a *[BehaviorError] (matching
// [ErrBehaviorViolation]). With [WithStrict] the mask is checked as
// ValidateStrict checks it: every path is enforced whatever
// WithPathValidation says, and every refusal is a *[StrictError] (matching
// [ErrInvalidUpdateMask], and its cause) naming the offending path, or the
// mask field of [WithUpdateMaskFieldName] for a mask-level refusal. On any
// error msg is left untouched.
func ApplyMask(raw *fieldmaskpb.FieldMask, msg proto.Message, opts ...Option) (fieldmask.FieldMask, error) {
	o := newOptions(opts...)
	if !o.strict {
		return applyMask(raw, msg, o)
	}

	o.pathValidation = PathValidationEnforce

	effective, err := applyMask(raw, msg, o)
	if err != nil {
		return nil, strictError(err, o.updateMaskFieldName)
	}

	return effective, nil
}

// applyMask is [ApplyMask] under o.
func applyMask(raw *fieldmaskpb.FieldMask, msg proto.Message, o *options) (fieldmask.FieldMask, error) {
	if msg == nil || !msg.ProtoReflect().IsValid() {
		return nil, ErrMessageMismatch
	}

	u, root, err := newApplyCheck(msg, o.current, o)
	if err != nil {
		return nil, err
	}

	paths := raw.GetPaths()
	if raw == nil || (len(paths) == 0 && o.emptyMaskImplied) {
		paths = impliedUpdateMask(msg, o).ToPaths()
	}

	if len(paths) == 0 {
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
		return fieldmask.FieldMask{}, nil // every path was dropped: nothing to update.
	case len(paths) == 1 && paths[0] == maskpath.Wildcard:
		work = u.replaceAll(md, root)
	default:
		work, covered, err = o.resolveRawPaths(paths, md)
		if err != nil {
			return nil, err
		}

		u.checkCovering(work, covered, md, root)
	}

	u.checkOneofSwitches(md, root, work, covered)

	if err := u.maskRefusal(work, md); err != nil {
		return nil, err
	}

	if len(u.violations) > 0 {
		return nil, &BehaviorError{Violations: u.violations}
	}

	if len(work) == 0 {
		return fieldmask.FieldMask{}, nil // every path was dropped: nothing to update.
	}

	effective := work.Clone()
	dropPolicyIdentifiers(effective, md, o)

	preserveIdentifierFields(work, msg, o)
	maskwalk.Filter(work, msg, false)

	if err := clearOutputOnly(msg, o); err != nil {
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
