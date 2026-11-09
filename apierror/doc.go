// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package apierror implements the AIP-193 error model without a gRPC
// dependency.
//
// An [Error] carries a google.rpc.Code, a developer-facing English message and
// the standard details of google/rpc/error_details.proto, ErrorInfo always
// among them: [New] validates the reason (UPPER_SNAKE_CASE, at most 63
// characters) and the metadata keys (lowerCamelCase, at most 64 characters),
// requires a locale and a message for a LocalizedMessage and admits each
// detail type once. DebugInfo is kept on the server: it is rendered only with
// [WithIncludeDebugInfo].
//
// [Error.Proto] renders a google.rpc.Status (a gRPC layer returns
// status.FromProto(e.Proto()).Err()), [Error.JSON] and [Error.WriteHTTP] the
// AIP-193 HTTP/JSON form, [HTTPStatus] maps a code to its HTTP status, and
// [FromProto] reads a status back.
//
// [FromError] and [Mapper] turn any error into an Error: the canonical
// sentinels of go-atlas-core/errors by [DefaultRules] (a FieldError becomes
// INVALID_ARGUMENT with a BadRequest field violation, ErrConflict ABORTED,
// ErrNotFound NOT_FOUND ...), other sentinels by the rules a service adds
// with [WithRules] — the aipmap subpackage holds the rules of the go-aip
// packages — and anything else INTERNAL without its text. A [Classified]
// error ([Classify]) reads as and unwraps to its category alone and hides its
// cause from the mapper and every other reader of the chain; [HiddenCause]
// returns it for logs. Error text the mapper copies into a status is cut to
// [MaxExposedMessageLength] runes.
//
// # Usage
//
//	var errNoStock = apierror.MustNew(code.Code_FAILED_PRECONDITION, "the item is out of stock",
//		apierror.WithReason("NO_STOCK"), apierror.WithDomain("shop.example.com"))
//
//	e, err := apierror.New(code.Code_INVALID_ARGUMENT, "the shelf name is invalid",
//		apierror.WithReason("INVALID_SHELF"), apierror.WithDomain("library.example.com"),
//		apierror.WithMetadata(map[string]string{"shelf": name}),
//		apierror.WithFieldViolations(&errdetails.BadRequest_FieldViolation{Field: "shelf.name", Description: "must not be empty"}),
//		apierror.WithLocalizedMessage("en-US", "Enter a shelf name."))
//
//	mapper, merr := apierror.NewMapper(apierror.WithRules(aipmap.Rules()...),
//		apierror.WithDefaultDomain("library.example.com"))
//	if merr != nil {
//		return merr
//	}
//	st := mapper.Map(handlerErr).Proto() // handlerErr is the error a handler failed with
package apierror
