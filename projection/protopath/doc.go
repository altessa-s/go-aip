// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package protopath validates read mask paths against the descriptor of a resource message, so the paths a projection policy
// accepts satisfy AIP-161 for that resource.
//
// A [Validator] walks each dot-separated path through the message descriptor. Its [Validator.Validate] method has the signature
// of projection.PathValidator and plugs into projection.WithPathValidator and projection.WithPathGrammar directly:
//
//	v, err := protopath.New((&pb.Book{}).ProtoReflect().Descriptor())
//	policy, err := projection.NewPolicy(projection.WithPathValidator(v.Validate))
//
// # Rules
//
//	Path                        Result
//	title, author.name          valid: proto field names relative to the resource
//	authors (repeated)          valid: a repeated field is a leaf
//	authors.0, authors.name     ErrInvalidFieldPath: a repeated field must be the last segment
//	labels (map)                valid: a map field is a leaf
//	labels.key, labels.`k`      ErrUnsupportedPath: map key paths are not served
//	create_time.seconds         ErrInvalidFieldPath: google.protobuf well-known types are leaves
//	title.length                ErrInvalidFieldPath: scalars and enums have no subfields
//	nickname                    ErrInvalidFieldPath + ErrUnknownField, unless WithIgnoreUnknown
//	displayName                 valid only with WithJSONNames
//	parent.parent.title         valid: recursive messages are walked segment by segment
//
// The walk is bounded by the path itself, so recursive messages never loop; [WithMaxDepth] caps the number of segments. A
// Validator is immutable and safe for concurrent use.
//
// See README.md for the complete guide.
package protopath
