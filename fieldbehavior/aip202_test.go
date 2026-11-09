// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldbehavior_test

import (
	"errors"
	"net/mail"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldinfo"

	infopb "github.com/altessa-s/go-aip/internal/proto/gen/fieldinfotest/v1"
)

const hostUUID = "f47ac10b-58cc-4372-a567-0e02b2c3d479"

func behaviorViolations(t *testing.T, err error) []fieldbehavior.BehaviorViolation {
	t.Helper()

	require.ErrorIs(t, err, fieldbehavior.ErrBehaviorViolation)

	ve, ok := errors.AsType[*fieldbehavior.BehaviorViolationError](err)
	require.True(t, ok)

	return ve.Violations
}

func formatPaths(vs []fieldbehavior.BehaviorViolation) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Path)
	}

	return out
}

func badHost() *infopb.Host {
	return &infopb.Host{
		Uid:             "nope",
		DnsServers:      []string{"1.1.1.1", "dns.example"},
		Peers:           map[string]string{"edge": "1.2.3.4"},
		Primary:         &infopb.Interface{Address: "bad", InterfaceId: "bad"},
		Interfaces:      []*infopb.Interface{{InterfaceId: "bad"}},
		NamedInterfaces: map[string]*infopb.Interface{"lan": {Address: "bad"}},
		RequestId:       "not-a-uuid",
		DisplayName:     "host",
	}
}

func TestStripCreate_ValidatesFormatsInTheSamePass(t *testing.T) {
	t.Parallel()

	h := badHost()
	before := proto.Clone(h)

	vs := behaviorViolations(t, fieldbehavior.StripCreate(h))
	assert.Equal(t, []string{
		"uid",
		"dns_servers.1",
		"peers.edge",
		"primary.address",
		"primary.interface_id",
		"interfaces.0.interface_id",
		"named_interfaces.lan.address",
	}, formatPaths(vs), "request_id is OUTPUT_ONLY: cleared, never validated")

	assert.Equal(t, fieldbehavior.BehaviorViolation{
		Path:   "uid",
		Reason: "value " + fieldinfo.ReasonUUID4 + " (UUID4)",
		Format: "UUID4",
	}, vs[0])
	assert.Equal(t, annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED, vs[1].Behavior)
	assert.True(t, proto.Equal(before, h), "checks before it mutates")

	err := fieldbehavior.StripCreate(h)
	assert.Contains(t, err.Error(), "fieldbehavior: 7 violation(s): uid value "+fieldinfo.ReasonUUID4+" (UUID4); dns_servers.1 value")
	assert.NotContains(t, err.Error(), "FIELD_BEHAVIOR_UNSPECIFIED")
}

func TestStripCreate_ValidFormatsStripped(t *testing.T) {
	t.Parallel()

	h := &infopb.Host{Uid: hostUUID, RequestId: "not-a-uuid", Primary: &infopb.Interface{Address: "::1"}}
	require.NoError(t, fieldbehavior.StripCreate(h))
	assert.Empty(t, h.GetRequestId())
	assert.Equal(t, hostUUID, h.GetUid())
}

func TestStripCreate_FormatsOff(t *testing.T) {
	t.Parallel()

	h := badHost()
	require.NoError(t, fieldbehavior.StripCreate(h, fieldbehavior.WithFieldFormats(false)))
	assert.Empty(t, h.GetRequestId())
	assert.Equal(t, "nope", h.GetUid())
}

func TestStrip_StrictReportsBehaviorAndFormat(t *testing.T) {
	t.Parallel()

	h := &infopb.Host{Uid: "bad", Primary: &infopb.Interface{InterfaceId: "bad"}}
	vs := behaviorViolations(t, fieldbehavior.Strip(h,
		fieldbehavior.WithBehaviors(annotations.FieldBehavior_IMMUTABLE), fieldbehavior.WithStrict(true)))
	assert.Equal(t, []string{"uid", "primary.interface_id"}, formatPaths(vs))
	assert.Equal(t, annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED, vs[0].Behavior)
	assert.Equal(t, annotations.FieldBehavior_IMMUTABLE, vs[1].Behavior, "a rejected field is not format-checked")
	assert.Equal(t, "UUID4", vs[0].Format)
	assert.Empty(t, vs[1].Format, "a behavior violation has no format")
}

func TestStripUpdate_Formats(t *testing.T) {
	t.Parallel()

	stored := &infopb.Host{Uid: hostUUID, Primary: &infopb.Interface{InterfaceId: hostUUID}}

	// The unchanged IMMUTABLE value is cleared, the bad address reported.
	h := &infopb.Host{Ipv4Address: "999.0.0.1", Primary: &infopb.Interface{InterfaceId: hostUUID}}
	vs := behaviorViolations(t, fieldbehavior.StripUpdate(h, fieldbehavior.WithCurrent(stored)))
	assert.Equal(t, []string{"ipv4_address"}, formatPaths(vs))

	h = &infopb.Host{Ipv4Address: "9.0.0.1", Primary: &infopb.Interface{InterfaceId: hostUUID}}
	require.NoError(t, fieldbehavior.StripUpdate(h, fieldbehavior.WithCurrent(stored)))
	assert.Empty(t, h.GetPrimary().GetInterfaceId())
}

func TestStrip_FormatOnlySubtreeIsWalked(t *testing.T) {
	t.Parallel()

	// No message in Host's tree carries INPUT_ONLY, so only formats make the
	// walk necessary.
	h := &infopb.Host{Parent: &infopb.Host{Ipv6Address: "1.2.3.4"}}
	vs := behaviorViolations(t, fieldbehavior.Strip(h, fieldbehavior.WithBehaviors(annotations.FieldBehavior_INPUT_ONLY)))
	assert.Equal(t, []string{"parent.ipv6_address"}, formatPaths(vs))

	require.NoError(t, fieldbehavior.StripResponse(h), "responses are not format-checked")
	require.Error(t, fieldbehavior.StripResponse(h, fieldbehavior.WithFieldFormats(true)))

	plain := &infopb.Plain{Name: "x", Child: &infopb.Plain{}}
	require.NoError(t, fieldbehavior.StripCreate(plain))
}

func TestStrip_FormatsWithCustomResolver(t *testing.T) {
	t.Parallel()

	resolver := fieldbehavior.ResolverFunc(func(protoreflect.FieldDescriptor) []annotations.FieldBehavior { return nil })
	h := &infopb.Host{Primary: &infopb.Interface{Address: "bad"}}
	vs := behaviorViolations(t, fieldbehavior.StripCreate(h, fieldbehavior.WithResolver(resolver)))
	assert.Equal(t, []string{"primary.address"}, formatPaths(vs))
}

func TestStrip_FormatsMaxDepth(t *testing.T) {
	t.Parallel()

	deep := &infopb.Host{Uid: hostUUID}
	for range 5 {
		deep = &infopb.Host{Parent: deep}
	}

	require.ErrorIs(t, fieldbehavior.StripCreate(deep, fieldbehavior.WithMaxDepth(2)), fieldbehavior.ErrMaxDepthExceeded)

	// A tree without behaviors or formats is not walked, however deep.
	plain := &infopb.Plain{}
	for range 5 {
		plain = &infopb.Plain{Child: plain}
	}

	require.NoError(t, fieldbehavior.StripCreate(plain, fieldbehavior.WithMaxDepth(2)))
	require.NoError(t, fieldbehavior.ValidateRequired(plain, fieldbehavior.WithMaxDepth(10)))
}

func TestValidateRequired_Formats(t *testing.T) {
	t.Parallel()

	h := badHost()
	h.DisplayName = ""

	vs := behaviorViolations(t, fieldbehavior.ValidateRequired(h))
	assert.Equal(t, []string{
		"uid",
		"dns_servers.1",
		"peers.edge",
		"primary.address",
		"primary.interface_id",
		"interfaces.0.interface_id",
		"named_interfaces.lan.address",
		"display_name",
	}, formatPaths(vs), "OUTPUT_ONLY request_id is skipped")
	assert.Equal(t, annotations.FieldBehavior_REQUIRED, vs[7].Behavior)

	require.ErrorIs(t, fieldbehavior.ValidateRequired(h, fieldbehavior.WithFieldFormats(false)), fieldbehavior.ErrBehaviorViolation)

	vs = behaviorViolations(t, fieldbehavior.ValidateRequired(h, fieldbehavior.WithFieldFormats(false)))
	assert.Equal(t, []string{"display_name"}, formatPaths(vs))

	require.NoError(t, fieldbehavior.ValidateRequired(&infopb.Host{Uid: hostUUID, DisplayName: "x"}))
}

func TestWithFormatChecker_CustomEmail(t *testing.T) {
	t.Parallel()

	var reg fieldinfo.Registry
	require.NoError(t, reg.Register("email", fieldinfo.CustomFormat{
		Validate: func(s string) error {
			_, err := mail.ParseAddress(s)
			return err
		},
		Reason: "must be an email address",
	}))
	require.NoError(t, reg.Assign("fieldinfotest.v1.Host.contact_email", "email"))

	checker := fieldinfo.NewChecker(fieldinfo.WithRegistry(&reg))
	h := &infopb.Host{ContactEmail: "nope", DisplayName: "x"}

	vs := behaviorViolations(t, fieldbehavior.ValidateRequired(h, fieldbehavior.WithFormatChecker(checker)))
	assert.Equal(t, []fieldbehavior.BehaviorViolation{{Path: "contact_email", Reason: "value must be an email address (email)", Format: "email"}}, vs)

	vs = behaviorViolations(t, fieldbehavior.StripCreate(h, fieldbehavior.WithFormatChecker(checker)))
	assert.Equal(t, []string{"contact_email"}, formatPaths(vs))

	require.NoError(t, fieldbehavior.ValidateRequired(h), "the default checker has no custom formats")
}
