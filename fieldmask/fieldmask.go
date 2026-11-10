// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldmask

import (
	"fmt"
	"slices"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask/internal/maskpath"
	"github.com/altessa-s/go-aip/fieldmask/internal/maskwalk"
	"github.com/altessa-s/go-aip/internal/behavior"

	coreslices "github.com/altessa-s/go-atlas-core/collections/slices"
)

const (
	// estimatedFieldsPerPath is the assumed average nesting depth per path,
	// used to estimate map capacity in [FromPaths].
	estimatedFieldsPerPath = 3

	// minMapCapacity is the minimum [FieldMask] map capacity to reduce early
	// rehashing.
	minMapCapacity = 8

	// reasonCoveredPath is the reason, formatted with the covering path, of
	// a raw path another path of the same mask covers.
	reasonCoveredPath = "path is covered by path %q of the same mask"
)

// FieldMask represents a hierarchical field mask as a nested map structure.
// Keys are field names; nil values indicate leaf fields (keep/prune the entire subtree).
// All methods are safe for nil or empty receivers and produce deterministic output.
type FieldMask map[string]FieldMask

// estimateMapCapacity estimates required map capacity based on path count.
func estimateMapCapacity(pathCount int) int {
	if pathCount == 0 {
		return 0
	}
	// Estimate: pathCount * average depth per path
	capacity := pathCount * estimatedFieldsPerPath
	// Use minimum capacity to avoid small map rehashing
	return max(capacity, minMapCapacity)
}

// estimatePathCount recursively estimates the number of paths in the mask.
func (msk FieldMask) estimatePathCount() int {
	count := 0

	for _, nested := range msk {
		if nested == nil {
			count++
		} else {
			count += nested.estimatePathCount()
		}
	}

	return count
}

// FromPaths creates a FieldMask from dot-separated field paths the caller
// trusts, such as paths written in code. Empty paths are filtered out and
// duplicates collapse. A FieldMask is a set of paths none of which covers
// another, so a path below another path of the list ("profile.id" with
// "profile") collapses into it unchecked: no later schema or field behavior
// check sees it. A malformed path ("a.", ".a", "a..b", an unclosed backtick)
// is kept verbatim as one segment, so it never widens to its parent nor
// vanishes, and every schema check ([FieldMask.Validate], [FieldMask.Filter],
// [github.com/altessa-s/go-aip/fieldmask/update.Apply], ...) rejects it. Build
// a mask from client input with [ParsePaths] or [FromProtoFieldMask], which
// reject malformed and covered paths up front.
//
// Example:
//
//	mask := fieldmask.FromPaths("user.name", "user.address.city")
func FromPaths(paths ...string) FieldMask {
	// Filter out empty paths to avoid processing them
	paths = slices.Collect(coreslices.Filter(paths, func(i string) bool {
		return i != ""
	}))

	if len(paths) == 0 {
		return make(FieldMask)
	}

	// Pre-allocate map with estimated capacity based on number of paths
	capacity := estimateMapCapacity(len(paths))
	mask := make(FieldMask, capacity)

	for _, path := range paths {
		maskwalk.Insert(mask, maskpath.Split(path))
	}

	return mask
}

// ParsePaths creates a FieldMask from raw client paths, checking each by the
// path grammar before it is normalized (AIP-161): an empty path, an empty
// segment ("a.", ".a", "a..b"), an unclosed backtick or a backtick inside an
// unquoted segment is a *[ValidationError] naming the path as written. The
// full-replacement path "*" combined with any other path, another "*"
// included, is a *[ValidationError] that also matches [ErrWildcardNotAlone].
// A path another path of the mask covers ("profile.id" with "profile",
// "aliases.*" with "aliases") is a *[ValidationError] naming the covered
// path: a FieldMask, a set of paths none of which covers another, could only
// drop it, and a dropped path would escape every later schema and field
// behavior check. An exact duplicate is accepted. To accept such a mask and
// check every path as sent, plan the raw mask with
// [github.com/altessa-s/go-aip/fieldmask/update.Spec.Plan]. The schema is not
// consulted; check the result with [FieldMask.Validate] or apply it with a
// method that validates.
//
// The default limits ([DefaultMaxPaths], [DefaultMaxPathLength],
// [DefaultMaxPathDepth]) are checked first, before any parsing: a mask
// beyond one is a *[ValidationError] matching [ErrLimitExceeded]. Use
// [FromProtoFieldMask] to set other limits.
//
// Example:
//
//	mask, err := fieldmask.ParsePaths(req.GetReadMask().GetPaths()...)
func ParsePaths(paths ...string) (FieldMask, error) {
	return parsePaths(paths, newOptions())
}

// parsePaths is [ParsePaths] under the limits of policy.
func parsePaths(paths []string, policy *options) (FieldMask, error) {
	if err := policy.validateMaskLimits(paths); err != nil {
		return nil, err
	}

	for _, p := range paths {
		if err := maskpath.CheckSyntax(p); err != nil {
			return nil, validationError(err)
		}

		if p == maskpath.Wildcard && len(paths) > 1 {
			return nil, wildcardNotAloneError()
		}
	}

	mask := FromPaths(paths...)
	if err := checkCovered(mask, paths); err != nil {
		return nil, err
	}

	return mask, nil
}

// checkCovered returns the *ValidationError of the first of paths that
// another path of mask, built from paths, covers ("profile.id" under
// "profile"): the mask no longer holds it, so no later check could see it.
// An exact duplicate is not covered.
func checkCovered(mask FieldMask, paths []string) error {
	for _, p := range paths {
		parts := maskpath.Split(p)
		if n := maskwalk.CoveredAt(mask, parts); n > 0 {
			return &ValidationError{Path: p, Reason: fmt.Sprintf(reasonCoveredPath, maskpath.Join(parts[:n]))}
		}
	}

	return nil
}

// FromProtoFieldMask creates a FieldMask from a protobuf FieldMask, checking
// its paths as [ParsePaths] does. It keeps the presence of the mask
// (AIP-134): a nil protoMask (update_mask omitted) is a nil FieldMask, and a
// protoMask with no paths (update_mask sent empty) is a non-nil empty one.
// [github.com/altessa-s/go-aip/fieldmask/update.Apply] and [github.com/altessa-s/go-aip/fieldmask/update.ValidateStrict] apply the
// implied mask for the first and treat the second as a no-op. The options
// set the limits checked before any parsing ([WithMaxPaths],
// [WithMaxPathLength], [WithMaxPathDepth]; the defaults otherwise).
//
// Example:
//
//	mask, err := fieldmask.FromProtoFieldMask(req.GetUpdateMask())
func FromProtoFieldMask(protoMask *fieldmaskpb.FieldMask, opts ...Option) (FieldMask, error) {
	if protoMask == nil {
		return nil, nil //nolint:nilnil // a nil FieldMask is the omitted update_mask (AIP-134).
	}

	return parsePaths(protoMask.GetPaths(), newOptions(opts...))
}

// wildcardNotAloneError is the *ValidationError of "*" combined with other
// paths.
func wildcardNotAloneError() error {
	return &ValidationError{Path: maskpath.Wildcard, Reason: maskpath.ReasonWildcardNotAlone, Cause: ErrWildcardNotAlone}
}

// ToProtoFieldMask converts the FieldMask to a protobuf FieldMask. It keeps
// the presence of the mask, as [FromProtoFieldMask] does: a nil FieldMask is
// a nil protobuf mask (omitted), and a non-nil empty one a mask with no paths.
func (msk FieldMask) ToProtoFieldMask() *fieldmaskpb.FieldMask {
	if msk == nil {
		return nil
	}

	return &fieldmaskpb.FieldMask{Paths: msk.ToPaths()}
}

// ToPaths converts the FieldMask to a flat list of dot-separated paths.
// Output order is deterministic (lexicographic).
func (msk FieldMask) ToPaths() []string {
	if len(msk) == 0 {
		return []string{}
	}

	paths := make([]string, 0, msk.estimatePathCount())
	msk.buildPaths("", &paths)

	// Map iteration order is randomized; sort for deterministic output.
	slices.Sort(paths)

	return paths
}

// Clone creates a deep copy of the FieldMask. It keeps the presence of the
// mask: a nil FieldMask (update_mask omitted) clones to nil, and a non-nil
// empty one to a non-nil empty one.
func (msk FieldMask) Clone() FieldMask {
	if msk == nil {
		return nil
	}

	if len(msk) == 0 {
		return make(FieldMask)
	}

	result := make(FieldMask, len(msk))
	for field, nested := range msk {
		if nested == nil {
			result[field] = nil
		} else {
			result[field] = nested.Clone()
		}
	}

	return result
}

// IsEmpty returns true if the field mask contains no fields, whether it is
// nil (an omitted update_mask, see [FromProtoFieldMask]) or non-nil and
// empty. After a successful [github.com/altessa-s/go-aip/fieldmask/update.Apply] on a non-nil mask it
// reports that the update changes nothing, as [github.com/altessa-s/go-aip/fieldmask/update.Plan.Empty] does.
func (msk FieldMask) IsEmpty() bool {
	return len(msk) == 0
}

// Contains checks if a dot-separated path exists in the FieldMask.
// Backtick-quoted segments are honored per AIP-161 (e.g. “reviews.`John Smith` “).
//
// Example:
//
//	exists := mask.Contains("user.address.city")
func (msk FieldMask) Contains(path string) bool {
	if path == "" || len(msk) == 0 {
		return false
	}

	parts := maskpath.Split(path)
	if len(parts) == 0 {
		return false
	}

	currentLevel := msk

	for i, part := range parts {
		if currentLevel == nil {
			// We need to go deeper, but the mask level is nil (leaf) or non-existent.
			return false
		}

		nested, specificExists := currentLevel[part]

		if !specificExists {
			// The specific part does not exist at this level
			return false
		}

		// If this is the last part of the path, we've found a match
		if i == len(parts)-1 {
			return true
		}

		// Continue to the next part of the path with the nested mask
		currentLevel = nested
	}

	// Should not be reached if parts is not empty
	return false
}

// SelectsAll reports whether msk is the full-replacement mask, the single
// path "*", which selects every field of the message
// (AIP-134). "*" combined with any other top-level path is a
// *[ValidationError] on "*" that matches [ErrWildcardNotAlone]. A mask
// without a top-level "*" selects only what it names; a nested "*" (such as
// "aliases.*") is an ordinary wildcard segment.
//
// A FieldMask is a set of paths, so a repeated "*" has already collapsed into
// one here; the raw-path entry points ([ParsePaths], [FromProtoFieldMask],
// and the update planning) reject "*" combined with anything, another "*" included.
func (msk FieldMask) SelectsAll() (bool, error) {
	if _, ok := msk[maskpath.Wildcard]; !ok {
		return false, nil
	}

	if len(msk) != 1 || msk[maskpath.Wildcard] != nil {
		return false, wildcardNotAloneError()
	}

	return true, nil
}

// Resolve validates msk against md by the path grammar (AIP-161) and returns
// it with every field segment spelled by its declared name, so a client may
// send a path in any of the spellings of its fields: the declared name
// ("tenant_id"), the JSON name ("tenantId") or the snake_case form protojson
// decodes a JSON mask path to (JSON "noisePercent7d" arrives as
// "noise_percent7d"). Map keys and wildcards are kept as written. A
// google.protobuf.Struct is addressed as a map<string, Value> ("extra_data",
// "extra_data.*", "extra_data.key"), and by default a path goes on into the
// dynamic JSON below ("extra_data.work_period.from", "list_data.*.name");
// [WithStrictDynamicPaths] rejects such deeper paths (AIP-161 alone). The
// options are those of the mask policy; [WithStrictDynamicPaths] and the
// limits ([WithMaxPaths], [WithMaxPathLength], [WithMaxPathDepth], checked
// first, an error matching [ErrLimitExceeded]) apply.
//
// The full-replacement mask "*" ([FieldMask.SelectsAll]) comes back as is,
// and "*" combined with other paths is an error matching
// [ErrWildcardNotAlone]. A path the grammar or the schema rejects — an index
// into a repeated field ("authors.0"), a field of its elements without "*"
// ("authors.given_name"), a map key that is not a valid value of the key
// type, an unknown field — is a *[ValidationError], as from
// [FieldMask.Validate]. md must not be nil.
func (msk FieldMask) Resolve(md protoreflect.MessageDescriptor, opts ...Option) (FieldMask, error) {
	return msk.resolve(md, newOptions(opts...))
}

// resolve is [FieldMask.Resolve] under policy.
func (msk FieldMask) resolve(md protoreflect.MessageDescriptor, policy *options) (FieldMask, error) {
	paths := msk.ToPaths()
	if err := policy.validateMaskLimits(paths); err != nil {
		return nil, err
	}

	all, err := msk.SelectsAll()
	if err != nil {
		return nil, err
	}

	if all {
		return msk.Clone(), nil
	}

	resolved := make([]string, 0, len(paths))
	for _, path := range paths {
		r, err := maskpath.ResolvePath(md, path, policy.strictDynamicPaths)
		if err != nil {
			return nil, validationError(err)
		}

		resolved = append(resolved, r)
	}

	return FromPaths(resolved...), nil
}

// buildPaths recursively traverses the mask and appends full paths.
// Segments containing a dot are wrapped in backticks per AIP-161 so the
// path round-trips through [FromPaths].
func (msk FieldMask) buildPaths(prefix string, paths *[]string) {
	for field, nested := range msk {
		currentPath := behavior.JoinPath(prefix, behavior.QuoteSegment(field))

		if nested == nil {
			// Leaf node, add the full path
			*paths = append(*paths, currentPath)
		} else {
			// Branch node, recurse deeper
			nested.buildPaths(currentPath, paths)
		}
	}
}
