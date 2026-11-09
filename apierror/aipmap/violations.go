// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package aipmap

import (
	"errors"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/altessa-s/go-aip/apierror"
	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldinfo"
	"github.com/altessa-s/go-aip/fieldmask/update"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// BehaviorReason returns the BadRequest field violation reason of a field
// that breaks the google.api.field_behavior b: "<BEHAVIOR>_FIELD", such as
// REQUIRED_FIELD, IMMUTABLE_FIELD, IDENTIFIER_FIELD or OUTPUT_ONLY_FIELD. It
// returns "" for FIELD_BEHAVIOR_UNSPECIFIED and a value without a name.
func BehaviorReason(b annotations.FieldBehavior) string {
	if b == annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED {
		return ""
	}

	if r := b.String() + "_FIELD"; apierror.ValidReason(r) {
		return r
	}

	return ""
}

// FormatReason returns the BadRequest field violation reason of a value
// that does not match the AIP-202 format named format: "INVALID_<FORMAT>",
// such as INVALID_UUID4, INVALID_IPV4, INVALID_IPV6 or INVALID_IPV4_OR_IPV6.
// A custom format name is upper-cased with every run of other characters
// than letters and digits turned into one underscore ("email" is
// INVALID_EMAIL). A name that leaves no valid reason is
// [ReasonInvalidFieldFormat].
func FormatReason(format string) string {
	var sb strings.Builder

	sb.WriteString("INVALID_")

	sep := false

	for _, c := range format {
		switch {
		case c >= 'a' && c <= 'z':
			c -= 'a' - 'A'
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		default:
			sep = true
			continue
		}

		if sep && sb.Len() > len("INVALID_") {
			sb.WriteByte('_')
		}

		sep = false

		sb.WriteRune(c)
	}

	if r := sb.String(); r != "INVALID_" && apierror.ValidReason(r) {
		return r
	}

	return ReasonInvalidFieldFormat
}

// violation is one entry of an error that lists several fields.
type violation struct {
	path, description, reason string
}

// withViolations adds to INVALID_ARGUMENT rules the conversion of an error
// that lists several fields into one field violation per path.
func withViolations(rules []apierror.Rule) []apierror.Rule {
	for i := range rules {
		reason := rules[i].Reason
		rules[i].Convert = func(err error) *apierror.Error { return convertViolations(err, reason) }
	}

	return rules
}

// convertViolations builds the error of a listing error in err: nil when
// err holds none, so the rule applies as written.
func convertViolations(err error, reason string) *apierror.Error {
	var (
		inner error
		vs    []violation
	)

	if ve, ok := errors.AsType[*fieldinfo.ViolationError](err); ok {
		inner, reason, vs = ve, ReasonInvalidFieldFormat, formatViolations(ve.Violations)
	} else if ve, ok := errors.AsType[*fieldinfo.ValueError](err); ok {
		return convertValueError(err, ve)
	} else if be, ok := errors.AsType[*fieldbehavior.BehaviorViolationError](err); ok {
		inner = be
		reason, vs = payloadViolations(be.Violations)
	} else if ue, ok := errors.AsType[*update.BehaviorError](err); ok {
		inner, vs = ue, maskViolations(ue.Violations, reason)
	} else if se, ok := errors.AsType[*update.StrictError](err); ok {
		inner, vs = se, maskViolations(se.Violations, reason)
	} else {
		return nil
	}

	msg, prefix := err.Error(), ""
	if fe, ok := errors.AsType[*coreerrs.FieldError](err); ok && fe.Field != "" && errors.Is(fe.Err, inner) {
		msg, prefix = fe.Field+": "+fe.Reason, fe.Field
	}

	out := make([]*errdetails.BadRequest_FieldViolation, 0, len(vs))
	for _, v := range vs {
		if field := joinField(prefix, v.path); field != "" {
			out = append(out, &errdetails.BadRequest_FieldViolation{Field: field, Description: v.description, Reason: v.reason})
		}
	}

	return newInvalid(err, msg, reason, out...)
}

// convertValueError builds the error of a single value that breaks its
// format; a FieldError around it names the field.
func convertValueError(err error, ve *fieldinfo.ValueError) *apierror.Error {
	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	if !ok || fe.Field == "" {
		return newInvalid(err, err.Error(), ReasonInvalidFieldFormat)
	}

	return newInvalid(err, fe.Field+": "+fe.Reason, ReasonInvalidFieldFormat,
		&errdetails.BadRequest_FieldViolation{Field: fe.Field, Description: fe.Reason, Reason: FormatReason(ve.Format)})
}

// newInvalid returns the INVALID_ARGUMENT error, nil when the text is not
// valid UTF-8 (the rule then applies with its fixed message). The message and
// the violation descriptions are cut to apierror.MaxExposedMessageLength runes.
func newInvalid(cause error, msg, reason string, vs ...*errdetails.BadRequest_FieldViolation) *apierror.Error {
	if strings.TrimSpace(msg) == "" {
		msg = cause.Error()
	}

	for _, v := range vs {
		v.Description = apierror.BoundMessage(v.Description)
	}

	e, err := apierror.New(code.Code_INVALID_ARGUMENT, apierror.BoundMessage(msg),
		apierror.WithReason(reason), apierror.WithFieldViolations(vs...), apierror.WithCause(cause))
	if err != nil {
		return nil
	}

	return e
}

// formatViolations are the violations of a fieldinfo walk.
func formatViolations(vs []fieldinfo.Violation) []violation {
	out := make([]violation, len(vs))
	for i, v := range vs {
		out[i] = violation{path: v.Path, description: v.Reason, reason: FormatReason(v.Format)}
	}

	return out
}

// payloadViolations are the violations of a fieldbehavior check and the
// ErrorInfo reason: [ReasonInvalidFieldFormat] when every violation is a
// format one (Behavior FIELD_BEHAVIOR_UNSPECIFIED), else
// [ReasonFieldBehaviorViolation].
func payloadViolations(vs []fieldbehavior.BehaviorViolation) (string, []violation) {
	reason := ReasonInvalidFieldFormat
	out := make([]violation, len(vs))

	for i, v := range vs {
		r := BehaviorReason(v.Behavior)
		if v.Behavior == annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED {
			r = FormatReason(v.Format)
		} else {
			reason = ReasonFieldBehaviorViolation
		}

		out[i] = violation{path: v.Path, description: describe(v.Reason, v.Behavior), reason: textOr(r, ReasonFieldBehaviorViolation)}
	}

	if len(vs) == 0 {
		reason = ReasonFieldBehaviorViolation
	}

	return reason, out
}

// maskViolations are the violations of a fieldmask update check: a
// violation without a behavior (a path that is not an update path) takes
// the rule's reason.
func maskViolations(vs []update.FieldViolation, reason string) []violation {
	out := make([]violation, len(vs))
	for i, v := range vs {
		out[i] = violation{path: v.Path, description: describe(v.Reason, v.Behavior), reason: textOr(BehaviorReason(v.Behavior), reason)}
	}

	return out
}

// describe is the field violation description: the reason, or the behavior
// name when the producer gave none.
func describe(reason string, b annotations.FieldBehavior) string {
	if reason != "" {
		return reason
	}

	return "breaks " + b.String()
}

// joinField appends the violation path to the field of an enclosing
// FieldError.
func joinField(prefix, path string) string {
	switch {
	case prefix == "":
		return path
	case path == "":
		return prefix
	default:
		return prefix + "." + path
	}
}

// textOr returns s, or def when s is empty.
func textOr(s, def string) string {
	if s == "" {
		return def
	}

	return s
}
