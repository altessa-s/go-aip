// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package resourcename

//go:generate optgen generate --type=validatorOptions --option-type=Option --output=options_gen.go
//go:generate optgen generate --type=checkOptions --option-type=CheckOption --output=check_options_gen.go

// Default limits of a [Validator], which bound the work a client name can
// cause before any matching. Zero disables a limit.
const (
	// DefaultMaxNameLength is the default maximum length of a name in
	// bytes, full names included.
	DefaultMaxNameLength = 4096
	// DefaultMaxSegments is the default maximum number of segments of a
	// relative name.
	DefaultMaxSegments = 64
)

// WildcardPolicy says where a check accepts the "-" wildcard of AIP-159 in
// place of a resource ID.
type WildcardPolicy int

const (
	// WildcardNone refuses "-" everywhere: Get, Create, Update, Delete and
	// every name a resource carries. It is the default.
	WildcardNone WildcardPolicy = iota
	// WildcardAny accepts "-" for any ID of the checked value: the parent
	// of a List reading across collections, such as "publishers/-".
	WildcardAny
	// WildcardAncestors accepts "-" for any ID except the last one of the
	// checked value: the unique resource lookup of AIP-159, such as a Get of
	// "publishers/-/books/les-miserables", which a service may offer only
	// when the resource ID is unique across parents.
	WildcardAncestors
)

// validatorOptions configures a [Validator].
type validatorOptions struct {
	// WithIDValidator replaces the check of every resource ID segment: the
	// default is [PermissiveID]; [StrictID] applies the RFC-1034
	// recommendation of AIP-122. Nil keeps the default.
	idValidator IDValidator `optgen:"default=PermissiveID"`
	// WithMaxNameLength bounds the bytes of a name. Zero is unlimited; a
	// negative value keeps the default.
	maxNameLength int `optgen:"default=DefaultMaxNameLength" optval:"positive=allow_zero"`
	// WithMaxSegments bounds the segments of a relative name. Zero is
	// unlimited; a negative value keeps the default.
	maxSegments int `optgen:"default=DefaultMaxSegments" optval:"positive=allow_zero"`
	// WithFullNames accepts full resource names ("//service/relative")
	// whose service is the resource type's, in addition to relative names.
	// By default a full name is refused.
	fullNames bool `optval:"param"`
}

// checkOptions configures one check of a [Validator].
type checkOptions struct {
	// WithWildcard sets where the check accepts the "-" wildcard of
	// AIP-159; the default is [WildcardNone]. [Validator.ValidateRequest]
	// applies it to every resource reference of the request, never to the
	// name a resource carries.
	wildcard WildcardPolicy
}
