// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldmask

//go:generate optgen generate --type=options --option-type=Option --output=options_gen.go

import "github.com/altessa-s/go-aip/internal/maskpath"

// options configures the path checks of the read entry points
// ([FromProtoFieldMask], [FieldMask.Resolve], [FieldMask.Validate],
// [FieldMask.Filter], [FieldMask.Prune], [FieldMask.Select], [ResolvePath]).
type options struct {
	// WithStrictDynamicPaths limits the paths into google.protobuf.Struct, Value and ListValue fields to the
	// AIP-161 rule: a Struct field whole, "*" or one key, and nothing deeper; a Value or ListValue whole only. By
	// default a path walks the dynamic JSON below a Struct key (an extension beyond AIP-161, see the path grammar).
	strictDynamicPaths bool `optval:"param"`
	// WithMaxPathDepth bounds the segments of a path ("*" and quoted keys included). Zero is unlimited; a negative
	// value keeps the default.
	maxPathDepth int `optgen:"default=DefaultMaxPathDepth" optval:"positive=allow_zero"`
	// WithMaxPaths bounds the paths of a mask. Zero is unlimited; a negative value keeps the default.
	maxPaths int `optgen:"default=DefaultMaxPaths" optval:"positive=allow_zero"`
	// WithMaxPathLength bounds the bytes of a raw path. Zero is unlimited; a negative value keeps the default.
	maxPathLength int `optgen:"default=DefaultMaxPathLength" optval:"positive=allow_zero"`
}

// limits returns the mask limits of o.
func (o *options) limits() maskpath.Limits {
	return maskpath.Limits{MaxPaths: o.maxPaths, MaxPathLength: o.maxPathLength, MaxPathDepth: o.maxPathDepth}
}

// validateMaskLimits reports the first limit paths break as a
// *ValidationError (none for the path count), or nil.
func (o *options) validateMaskLimits(paths []string) error {
	if v := o.limits().CheckMask(paths); v != nil {
		return &ValidationError{Path: v.Path, Reason: v.Reason, Cause: v.Cause}
	}

	return nil
}
