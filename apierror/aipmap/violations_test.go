// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package aipmap_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"

	"github.com/altessa-s/go-aip/apierror"
	"github.com/altessa-s/go-aip/apierror/aipmap"
	"github.com/altessa-s/go-aip/batch"
	"github.com/altessa-s/go-aip/etag"
	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldinfo"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	fipb "github.com/altessa-s/go-aip/internal/proto/gen/fieldinfotest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// violation is a field violation as field, reason.
type violation struct{ field, reason string }

func violations(e *apierror.Error) []violation {
	out := make([]violation, 0, len(e.FieldViolations()))
	for _, v := range e.FieldViolations() {
		out = append(out, violation{v.GetField(), v.GetReason()})
	}

	return out
}

func TestFormatReason(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"UUID4":        "INVALID_UUID4",
		"IPV4":         "INVALID_IPV4",
		"IPV6":         "INVALID_IPV6",
		"IPV4_OR_IPV6": "INVALID_IPV4_OR_IPV6",
		"email":        "INVALID_EMAIL",
		"e-mail/v2 ":   "INVALID_E_MAIL_V2",
		"__x__":        "INVALID_X",
		"":             aipmap.ReasonInvalidFieldFormat,
		"--":           aipmap.ReasonInvalidFieldFormat,
		"é":            aipmap.ReasonInvalidFieldFormat,
		"a123456789012345678901234567890123456789012345678901234567890": aipmap.ReasonInvalidFieldFormat,
	}

	for format, want := range tests {
		assert.Equal(t, want, aipmap.FormatReason(format), "format %q", format)
	}
}

func TestBehaviorReason(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "REQUIRED_FIELD", aipmap.BehaviorReason(annotations.FieldBehavior_REQUIRED))
	assert.Equal(t, "IMMUTABLE_FIELD", aipmap.BehaviorReason(annotations.FieldBehavior_IMMUTABLE))
	assert.Equal(t, "IDENTIFIER_FIELD", aipmap.BehaviorReason(annotations.FieldBehavior_IDENTIFIER))
	assert.Equal(t, "OUTPUT_ONLY_FIELD", aipmap.BehaviorReason(annotations.FieldBehavior_OUTPUT_ONLY))
	assert.Empty(t, aipmap.BehaviorReason(annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED))
	assert.Empty(t, aipmap.BehaviorReason(annotations.FieldBehavior(999)), "an unnamed value has no reason")
}

func TestFieldInfoViolations(t *testing.T) {
	t.Parallel()

	m := newMapper(t)

	host := &fipb.Host{Uid: "not-a-uuid", DnsServers: []string{"1.1.1.1", "nope"}, Ipv4Address: "::1"}
	err := fieldinfo.Validate(host, fieldinfo.WithJSONNames(true))
	require.Error(t, err)

	e := m.Map(err)
	assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
	assert.Equal(t, aipmap.ReasonInvalidFieldFormat, e.Reason())
	assert.Equal(t, err.Error(), e.Message())
	assert.NotContains(t, e.Message(), "not-a-uuid", "values are never echoed")
	assert.Equal(t, []violation{
		{"uid", "INVALID_UUID4"},
		{"ipv4Address", "INVALID_IPV4"},
		{"dnsServers.1", "INVALID_IPV4_OR_IPV6"},
	}, violations(e))

	for _, v := range e.FieldViolations() {
		assert.NotEmpty(t, v.GetDescription())
	}

	e = m.Map(batch.NewItemError("requests", 2, err))
	assert.Equal(t, aipmap.ReasonInvalidFieldFormat, e.Reason())
	assert.Equal(t, []violation{
		{"requests[2].uid", "INVALID_UUID4"},
		{"requests[2].ipv4Address", "INVALID_IPV4"},
		{"requests[2].dnsServers.1", "INVALID_IPV4_OR_IPV6"},
	}, violations(e), "a batch item prefixes every path")
	assert.Contains(t, e.Message(), "requests[2]: ")

	custom := &fieldinfo.ViolationError{Violations: []fieldinfo.Violation{
		{Path: "contactEmail", Format: "email", Reason: "must be an email address"},
		{Path: "", Format: "email", Reason: "no path"},
	}}
	e = m.Map(&coreerrs.FieldError{Field: "host", Reason: "invalid", Err: custom})
	assert.Equal(t, []violation{{"host.contactEmail", "INVALID_EMAIL"}, {"host", "INVALID_EMAIL"}}, violations(e))
	assert.Equal(t, "host: invalid", e.Message())

	e = m.Map(&fieldinfo.ViolationError{Violations: []fieldinfo.Violation{{Path: "", Format: "UUID4", Reason: "x"}}})
	assert.Empty(t, e.FieldViolations(), "a violation without a path names no field")
}

func TestFieldInfoValueError(t *testing.T) {
	t.Parallel()

	m := newMapper(t)

	verr := fieldinfo.ValidateValue(annotations.FieldInfo_UUID4, "nope")
	require.Error(t, verr)

	e := m.Map(verr)
	assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
	assert.Equal(t, aipmap.ReasonInvalidFieldFormat, e.Reason())
	assert.Empty(t, e.FieldViolations())
	assert.Equal(t, verr.Error(), e.Message())

	e = m.Map(&coreerrs.FieldError{Field: "book.uid", Reason: "must be a UUID4", Err: verr})
	assert.Equal(t, "book.uid: must be a UUID4", e.Message())
	assert.Equal(t, []violation{{"book.uid", "INVALID_UUID4"}}, violations(e))
}

// TestValueErrorMessageIsBounded pins that the text aipmap copies into the status is cut to
// apierror.MaxExposedMessageLength runes, the field violation description included.
func TestValueErrorMessageIsBounded(t *testing.T) {
	t.Parallel()

	m := newMapper(t)
	huge := strings.Repeat("ü", 4*apierror.MaxExposedMessageLength)

	e := m.Map(&fieldinfo.ValueError{Format: "UUID4", Reason: huge})
	require.NotNil(t, e)
	assert.Equal(t, apierror.MaxExposedMessageLength, utf8.RuneCountInString(e.Message()))
	assert.True(t, strings.HasSuffix(e.Message(), apierror.TruncatedSuffix))

	e = m.Map(&coreerrs.FieldError{Field: "book.uid", Reason: huge, Err: &fieldinfo.ValueError{Format: "UUID4", Reason: "x"}})
	require.NotNil(t, e)
	assert.Equal(t, apierror.MaxExposedMessageLength, utf8.RuneCountInString(e.Message()))
	require.Len(t, e.FieldViolations(), 1)
	assert.Equal(t, apierror.MaxExposedMessageLength, utf8.RuneCountInString(e.FieldViolations()[0].GetDescription()))
}

func TestFieldBehaviorViolations(t *testing.T) {
	t.Parallel()

	m := newMapper(t)

	err := fieldbehavior.ValidateRequired(&fipb.Host{Uid: "bad"})
	require.Error(t, err)

	e := m.Map(err)
	assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
	assert.Equal(t, aipmap.ReasonFieldBehaviorViolation, e.Reason())
	assert.ElementsMatch(t, []violation{{"uid", "INVALID_UUID4"}, {"display_name", "REQUIRED_FIELD"}}, violations(e),
		"a format violation names its format, a behavior violation its behavior")

	err = fieldbehavior.ValidateRequired(&fipb.Host{Uid: "bad", DisplayName: "edge"})
	require.Error(t, err)

	e = m.Map(err)
	assert.Equal(t, aipmap.ReasonInvalidFieldFormat, e.Reason(), "only format violations")
	assert.Equal(t, []violation{{"uid", "INVALID_UUID4"}}, violations(e))

	e = m.Map(&fieldbehavior.BehaviorViolationError{Violations: []fieldbehavior.BehaviorViolation{
		{Path: "name", Behavior: annotations.FieldBehavior_IDENTIFIER},
		{Path: "odd", Reason: "no format here"},
		{Path: "bad", Behavior: annotations.FieldBehavior(999)},
		{Path: "text", Reason: "value looks wrong (UUID4)"},
		{Path: "contactEmail", Reason: "must be an email address", Format: "email"},
	}})
	assert.Equal(t, []violation{
		{"name", "IDENTIFIER_FIELD"}, {"odd", aipmap.ReasonInvalidFieldFormat}, {"bad", aipmap.ReasonFieldBehaviorViolation},
		{"text", aipmap.ReasonInvalidFieldFormat}, {"contactEmail", "INVALID_EMAIL"},
	}, violations(e), "the format comes from Format, never from the reason text")
	assert.Equal(t, "breaks IDENTIFIER", e.FieldViolations()[0].GetDescription())

	e = m.Map(&fieldbehavior.BehaviorViolationError{})
	assert.Equal(t, aipmap.ReasonFieldBehaviorViolation, e.Reason())
	assert.Empty(t, e.FieldViolations())
}

func TestFieldMaskViolations(t *testing.T) {
	t.Parallel()

	m := newMapper(t)

	e := m.Map(fmt.Errorf("update: %w", &update.BehaviorError{Violations: []update.FieldViolation{
		{Path: "title", Behavior: annotations.FieldBehavior_REQUIRED, Reason: "required field is empty"},
		{Path: "isbn", Behavior: annotations.FieldBehavior_IMMUTABLE, Reason: "immutable field cannot be changed"},
	}}))
	assert.Equal(t, aipmap.ReasonFieldBehaviorViolation, e.Reason())
	assert.Equal(t, []violation{{"title", "REQUIRED_FIELD"}, {"isbn", "IMMUTABLE_FIELD"}}, violations(e))
	assert.Equal(t, "required field is empty", e.FieldViolations()[0].GetDescription())

	e = m.Map(&update.StrictError{Violations: []update.FieldViolation{
		{Path: "nope", Reason: "unknown field"},
		{Path: "name", Behavior: annotations.FieldBehavior_IDENTIFIER, Reason: "identifier"},
	}})
	assert.Equal(t, aipmap.ReasonFieldBehaviorViolation, e.Reason(), "a behavior violation wins")
	assert.Equal(t, []violation{{"nope", aipmap.ReasonFieldBehaviorViolation}, {"name", "IDENTIFIER_FIELD"}}, violations(e))

	e = m.Map(&update.StrictError{
		Cause:      fieldmask.ErrMaxPathsExceeded,
		Violations: []update.FieldViolation{{Path: "update_mask", Reason: "too many paths"}},
	})
	assert.Equal(t, aipmap.ReasonFieldMaskTooComplex, e.Reason())
	assert.Equal(t, []violation{{"update_mask", aipmap.ReasonFieldMaskTooComplex}}, violations(e))

	e = m.Map(&update.StrictError{Violations: []update.FieldViolation{{Path: "x\xff", Reason: "bad"}}})
	assert.Equal(t, aipmap.ReasonInvalidFieldMask, e.Reason(), "a path that is not UTF-8 falls back to the rule")
	assert.Empty(t, e.FieldViolations())
}

func TestEtagErrors(t *testing.T) {
	t.Parallel()

	m := newMapper(t)
	pre := etag.NewPrecondition(etag.WithField("book.etag"), etag.WithRequired(true))

	_, err := pre.Parse("")
	e := m.Map(err)
	assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
	assert.Equal(t, aipmap.ReasonEtagRequired, e.Reason())
	assert.Equal(t, []violation{{"book.etag", aipmap.ReasonEtagRequired}}, violations(e))

	_, err = pre.Parse(`W/"x`)
	e = m.Map(err)
	assert.Equal(t, aipmap.ReasonInvalidEtag, e.Reason())
	assert.Equal(t, []violation{{"book.etag", aipmap.ReasonInvalidEtag}}, violations(e))

	_, _, err = pre.ParseRevision(`"strong"`)
	e = m.Map(err)
	assert.Equal(t, aipmap.ReasonInvalidEtag, e.Reason(), "a tag the server did not issue")

	err = pre.Check(etag.FromRevision[int64](1).String(), etag.FromRevision[int64](2))
	e = m.Map(err)
	assert.Equal(t, code.Code_ABORTED, e.Code(), "AIP-154: a mismatch is ABORTED")
	assert.Equal(t, apierror.ReasonVersionConflict, e.Reason())
	assert.Equal(t, apierror.MessageConflict, e.Message())
}

func TestFieldViolationDescriptionsArePlain(t *testing.T) {
	t.Parallel()

	e := newMapper(t).Map(&fieldinfo.ViolationError{Violations: []fieldinfo.Violation{
		{Path: "uid", Format: "UUID4", Reason: "must be a UUID4"},
	}})
	require.Len(t, e.FieldViolations(), 1)
	assert.True(t, proto.Equal(
		&errdetails.BadRequest_FieldViolation{Field: "uid", Description: "must be a UUID4", Reason: "INVALID_UUID4"},
		e.FieldViolations()[0]))
}
