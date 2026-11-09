// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package softdelete

//go:generate optgen generate --type=policyOptions --option-type=Option --output=options_gen.go
//go:generate optgen generate --type=deleteOptions --option-type=DeleteOption --output=delete_options_gen.go
//go:generate optgen generate --type=purgerOptions --option-type=PurgerOption --output=purger_options_gen.go

import (
	"time"

	"github.com/altessa-s/go-atlas-core/time/clock"
)

// Standard field names of AIP-148 and AIP-164.
const (
	// FieldDeleteTime is the resource field holding the soft-delete time.
	FieldDeleteTime = "delete_time"
	// FieldPurgeTime is the resource field holding the time a soft-deleted
	// resource is purged.
	FieldPurgeTime = "purge_time"
	// FilterFieldDeleteTime is the public (JSON) name of delete_time in an
	// AIP-160 filter.
	FilterFieldDeleteTime = "deleteTime"
)

// Defaults of a [Policy] and a [Purger].
const (
	// DefaultRetention is how long a soft-deleted resource is kept before it
	// is purged: purge_time is delete_time plus the retention.
	DefaultRetention = 30 * 24 * time.Hour
	// DefaultSampleSize is the number of names a dry-run purge returns in
	// purgeSample (AIP-165 recommends about 100).
	DefaultSampleSize = 100
	// DefaultMaxPurgeCount is the largest number of resources a forced purge
	// may delete.
	DefaultMaxPurgeCount = 10000
	// DefaultMaxFilterLength bounds the bytes of a purge filter.
	DefaultMaxFilterLength = 4096
)

// policyOptions configures a [Policy].
type policyOptions struct {
	// WithDeleteTimeField names the google.protobuf.Timestamp field stamped on
	// Delete and cleared on Undelete (proto name).
	deleteTimeField string `optgen:"default=FieldDeleteTime" optval:"trimspaces"`
	// WithPurgeTimeField names the google.protobuf.Timestamp field holding the
	// purge time (proto name), "expire_time" for a resource that names it so.
	// A resource without the field gets no purge time.
	purgeTimeField string `optgen:"default=FieldPurgeTime" optval:"trimspaces"`
	// WithFilterField names delete_time in an AIP-160 filter: the public name
	// of the list schema field that Restrict tests.
	filterField string `optgen:"default=FilterFieldDeleteTime" optval:"trimspaces"`
	// WithRetention is the time between the soft delete and the purge. Zero
	// keeps a soft-deleted resource indefinitely (no purge time is stamped);
	// a negative value keeps the default.
	retention time.Duration `optgen:"default=DefaultRetention" optval:"positive=allow_zero"`
	// WithClock is the time source of delete and purge times. Nil keeps the
	// system clock.
	clock clock.Clock `optgen:"default=clock.Real()"`
	// WithHideDeletedOnGet makes Get of a soft-deleted resource NOT_FOUND. By
	// default Get returns it, as AIP-164 recommends.
	hideDeletedOnGet bool `optval:"param"`
	// WithOutputOnlyNotRequired accepts delete and purge time fields without
	// the OUTPUT_ONLY field behavior AIP-148 requires.
	outputOnlyNotRequired bool `optval:"param"`
}

// deleteOptions configures one [Policy.Delete] call.
type deleteOptions struct {
	// WithAllowMissing makes Delete of a missing or already soft-deleted
	// resource succeed without a change (AIP-135, AIP-164).
	allowMissing bool `optval:"param"`
	// WithPrecondition checks the stored resource (an AIP-154 etag, say)
	// before it is soft-deleted; its error is returned as is.
	precondition Precondition
}

// purgerOptions configures a [Purger].
type purgerOptions struct {
	// WithSampleSize bounds the names of purgeSample on a dry run. Zero
	// returns no sample; a negative value keeps the default.
	sampleSize int `optgen:"default=DefaultSampleSize" optval:"positive=allow_zero"`
	// WithMaxPurgeCount bounds the resources a forced purge may delete: the
	// matching resources are counted first and more than the limit is a
	// field error on filter. Zero is unlimited; a negative value keeps the
	// default.
	maxPurgeCount int64 `optgen:"default=DefaultMaxPurgeCount" optval:"positive=allow_zero"`
	// WithMaxFilterLength bounds the bytes of the filter, checked before
	// parsing. Zero is unlimited; a negative value keeps the default.
	maxFilterLength int `optgen:"default=DefaultMaxFilterLength" optval:"positive=allow_zero"`
	// WithParentRequired rejects a request without a parent: the purged
	// collection is a child collection.
	parentRequired bool `optval:"param"`
	// WithWildcard accepts the filter "*", which matches every resource of
	// the parent (AIP-165).
	wildcard bool `optval:"param"`
}
