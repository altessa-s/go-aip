// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package fieldbehavior applies the google.api.field_behavior annotations of AIP-203 to protobuf payloads.
//
// StripCreate clears OUTPUT_ONLY and IDENTIFIER fields and ValidateRequired reports absent or empty REQUIRED fields. StripUpdate clears
// OUTPUT_ONLY fields and compares IMMUTABLE and IDENTIFIER ones with the stored resource of WithCurrent: validated protected values are
// retained for subsequent planning and merging, and changed values are violations. Without WithCurrent protected values are kept for later
// comparison by update.Spec.Plan or update.Patch.Run; IDENTIFIER also lets the handler locate the resource. StripResponse clears INPUT_ONLY.
// OUTPUT_ONLY values are cleared at every depth and never reported, strict mode included. Input walks also report string values that break
// their AIP-202 google.api.field_info format in the same pass
// (WithFieldFormats, on by default; StripResponse turns it off). The Default*Behaviors functions return owned slices; Strip with
// WithBehaviors selects a custom set, and an explicitly empty set disables stripping.
//
// # Usage
//
//	if err := fieldbehavior.StripCreate(resource); err != nil { return err }
//	if err := fieldbehavior.ValidateRequired(resource); err != nil { return err }
//	if err := fieldbehavior.StripUpdate(resource, fieldbehavior.WithCurrent(stored)); err != nil { return err }
//	if err := fieldbehavior.StripResponse(response); err != nil { return err }
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package fieldbehavior
