// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package fieldinfo applies the google.api.field_info annotation of AIP-202 to protobuf payloads.
//
// Lookup and FormatOf read the annotation of a field descriptor (cached per descriptor): its format (UUID4, IPV4, IPV6, IPV4_OR_IPV6) and
// its referenced types, which are exposed only. ValidateValue, NormalizeValue and Equal work on one value: they validate it against its
// RFC, return the normalized form AIP-202 allows (lowercase UUID, IPv4 without leading zeros, RFC 5952 IPv6), and compare two values by
// their normalized forms instead of as text. Validate walks a message — repeated elements, map values and nested messages included — and
// reports every invalid value with its field path; Normalize validates and then rewrites the values in their normalized form, for input
// about to be persisted. AIP-202 has no email format: a Registry of caller-named custom formats, assigned to fields by full name and passed
// with WithRegistry, covers such values. The fieldbehavior package checks formats in the same walk as its AIP-203 rules.
//
// # Usage
//
//	if err := fieldinfo.Validate(req.GetHost()); err != nil { return err } // *ViolationError, INVALID_ARGUMENT
//	if err := fieldinfo.Normalize(req.GetHost()); err != nil { return err } // validated and normalized in place
//	same, err := fieldinfo.Equal(annotations.FieldInfo_IPV6, stored, given)
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package fieldinfo
