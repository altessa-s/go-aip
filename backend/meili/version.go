// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili

import (
	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Version is a Meilisearch server release, major.minor ([WithFilterServerVersion]). The zero value stands for the latest
// release.
type Version struct {
	Major int
	Minor int
}

// The releases that introduced the operators the translator emits beyond the 1.0 baseline (=, !=, <, <=, >, >=, IN,
// EXISTS, NOT, AND, OR).
const (
	// minorNullEmpty (1.2) introduced IS NULL and IS EMPTY.
	minorNullEmpty = 2
	// minorBackslashEscape (1.4) made the filter parser unescape `\\` in a quoted string; 1.0 to 1.3 unescape only `\"`.
	minorBackslashEscape = 4
	// minorContains (1.10) introduced CONTAINS behind the experimental containsFilter feature.
	minorContains = 10
	// minorStartsWith (1.11) introduced STARTS WITH behind the experimental containsFilter feature.
	minorStartsWith = 11
	// minorStartsWithStable (1.17) made STARTS WITH available without the experimental feature.
	minorStartsWithStable = 17
)

var (
	versionNullEmpty        = Version{Major: 1, Minor: minorNullEmpty}
	versionBackslashEscape  = Version{Major: 1, Minor: minorBackslashEscape}
	versionContains         = Version{Major: 1, Minor: minorContains}
	versionStartsWith       = Version{Major: 1, Minor: minorStartsWith}
	versionStartsWithStable = Version{Major: 1, Minor: minorStartsWithStable}
)

// atLeast reports whether v is min or later; the zero Version is the latest release.
func (v Version) atLeast(minimum Version) bool {
	if v == (Version{}) {
		return true
	}

	return v.Major > minimum.Major || v.Major == minimum.Major && v.Minor >= minimum.Minor
}

// features are the optional operators the configured server accepts.
type features struct {
	nullEmpty       bool // IS NULL, IS EMPTY
	backslashEscape bool // `\\` in a quoted string reads as one backslash
	contains        bool // CONTAINS
	startsWith      bool // STARTS WITH
}

// newFeatures resolves the operators of version, with the experimental containsFilter feature on or off.
func newFeatures(version Version, containsFilter bool) (features, error) {
	if version != (Version{}) && (version.Major < 1 || version.Minor < 0) {
		return features{}, coreerrs.Wrapf(filter.ErrInvalidOptions, "Meilisearch version %d.%d: 1.0 or later is required",
			version.Major, version.Minor)
	}

	if containsFilter && !version.atLeast(versionContains) {
		return features{}, coreerrs.Wrapf(filter.ErrInvalidOptions,
			"Meilisearch version %d.%d has no containsFilter feature (added in 1.10)", version.Major, version.Minor)
	}

	return features{
		nullEmpty:       version.atLeast(versionNullEmpty),
		backslashEscape: version.atLeast(versionBackslashEscape),
		contains:        containsFilter,
		startsWith: version.atLeast(versionStartsWithStable) ||
			containsFilter && version.atLeast(versionStartsWith),
	}, nil
}
