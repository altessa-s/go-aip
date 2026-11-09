// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldinfo_test

import (
	"errors"
	"net/mail"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/altessa-s/go-aip/fieldinfo"

	testpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldinfotest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

const (
	validUUID  = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	upperUUID  = "F47AC10B-58CC-4372-A567-0E02B2C3D479"
	emailField = protoreflect.FullName("fieldinfotest.v1.Host.contact_email")
)

func validHost() *testpb.Host {
	return &testpb.Host{
		Uid:         validUUID,
		Ipv4Address: "192.0.2.1",
		Ipv6Address: "2001:db8::1",
		IpAddress:   "10.0.0.1",
		DnsServers:  []string{"1.1.1.1", "2606:4700:4700::1111"},
		Peers:       map[string]string{"edge": "fe80::1"},
		Primary:     &testpb.Interface{Mac: "not-validated", Address: "10.0.0.2", InterfaceId: validUUID},
		Interfaces:  []*testpb.Interface{{Address: "::1"}},
		NamedInterfaces: map[string]*testpb.Interface{
			"lan": {Address: "192.168.0.1"},
		},
		ContactEmail: "ops@example.com",
		BackupIpv4:   new("192.0.2.2"),
		RequestId:    validUUID,
		RawAddress:   []byte("not an address"),
		DisplayName:  "host",
		Parent:       &testpb.Host{Uid: validUUID},
		Endpoint:     &testpb.Host_EndpointIpv6{EndpointIpv6: "::2"},
	}
}

func violations(t *testing.T, err error) []fieldinfo.Violation {
	t.Helper()

	require.ErrorIs(t, err, fieldinfo.ErrInvalidValue)
	require.ErrorIs(t, err, coreerrs.ErrInvalidArgument)

	ve, ok := errors.AsType[*fieldinfo.ViolationError](err)
	require.True(t, ok)

	return ve.Violations
}

func paths(vs []fieldinfo.Violation) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Path)
	}

	return out
}

func TestValidate_Valid(t *testing.T) {
	t.Parallel()

	require.NoError(t, fieldinfo.Validate(validHost()))
	require.NoError(t, fieldinfo.Validate(nil))
	require.NoError(t, fieldinfo.Validate(&testpb.Host{}), "unset fields are not validated")
	require.NoError(t, fieldinfo.Validate(&testpb.Plain{Name: "x", Child: &testpb.Plain{}}))
	require.NoError(t, fieldinfo.Validate((*testpb.Host)(nil)))
}

func TestValidate_ReportsEveryShape(t *testing.T) {
	t.Parallel()

	h := validHost()
	h.Uid = "F47AC10B-58CC-0372-8567-0E02B2C3D479"
	h.Ipv4Address = "256.0.0.1"
	h.Ipv6Address = "fe80::1%eth0"
	h.IpAddress = "example.com"
	h.DnsServers = []string{"1.1.1.1", "bad", ""}
	h.Peers = map[string]string{"edge": "1.2.3.4", "a.b": "::1", "": "x"}
	h.Primary.Address = "nope"
	h.Interfaces = append(h.Interfaces, &testpb.Interface{InterfaceId: "x"})
	h.NamedInterfaces["w`an"] = &testpb.Interface{Address: "300.1.1.1"}
	h.BackupIpv4 = new("")
	h.RequestId = "r"
	h.Parent = &testpb.Host{Ipv6Address: "1.2.3.4"}
	h.Endpoint = &testpb.Host_EndpointIpv4{EndpointIpv4: "1.2.3"}

	vs := violations(t, fieldinfo.Validate(h))
	assert.Equal(t, []string{
		"uid",
		"ipv4_address",
		"ipv6_address",
		"ip_address",
		"dns_servers.1",
		"dns_servers.2",
		"peers.``",
		"peers.edge",
		"primary.address",
		"interfaces.1.interface_id",
		"named_interfaces.`w``an`.address",
		"backup_ipv4",
		"request_id",
		"parent.ipv6_address",
		"endpoint_ipv4",
	}, paths(vs))

	assert.Equal(t, fieldinfo.Violation{Path: "uid", Format: "UUID4", Reason: fieldinfo.ReasonUUID4}, vs[0])
	assert.Equal(t, "IPV4", vs[1].Format)
	assert.Equal(t, "IPV6", vs[2].Format)
	assert.Equal(t, fieldinfo.ReasonIPv4OrIPv6, vs[3].Reason)

	err := fieldinfo.Validate(h)
	assert.Contains(t, err.Error(), "fieldinfo: 15 violation(s): uid (UUID4) "+fieldinfo.ReasonUUID4+"; ipv4_address (IPV4)")
	assert.NotContains(t, err.Error(), "256.0.0.1", "values are never echoed")
}

func TestValidate_JSONNames(t *testing.T) {
	t.Parallel()

	h := validHost()
	h.DnsServers = []string{"bad"}
	h.NamedInterfaces["lan"].Address = "bad"

	vs := violations(t, fieldinfo.Validate(h, fieldinfo.WithJSONNames(true)))
	assert.Equal(t, []string{"dnsServers.0", "namedInterfaces.lan.address"}, paths(vs))
}

func TestValidate_MaxDepth(t *testing.T) {
	t.Parallel()

	deep := &testpb.Host{Uid: "bad"}
	for range 5 {
		deep = &testpb.Host{Parent: deep}
	}

	require.ErrorIs(t, fieldinfo.Validate(deep, fieldinfo.WithMaxDepth(3)), fieldinfo.ErrMaxDepthExceeded)
	require.ErrorIs(t, fieldinfo.Normalize(deep, fieldinfo.WithMaxDepth(3)), fieldinfo.ErrMaxDepthExceeded)

	vs := violations(t, fieldinfo.Validate(deep, fieldinfo.WithMaxDepth(0)))
	assert.Equal(t, []string{"parent.parent.parent.parent.parent.uid"}, paths(vs))

	vs = violations(t, fieldinfo.Validate(deep, fieldinfo.WithMaxDepth(-1)))
	assert.Len(t, vs, 1, "a negative depth keeps the default")

	// A message without formats is never walked, however deep.
	plain := &testpb.Plain{}
	for range 10 {
		plain = &testpb.Plain{Child: plain}
	}

	require.NoError(t, fieldinfo.Validate(plain, fieldinfo.WithMaxDepth(1)))
}

func TestNormalize(t *testing.T) {
	t.Parallel()

	h := &testpb.Host{
		Uid:             upperUUID,
		Ipv4Address:     "010.000.000.001",
		Ipv6Address:     "2001:0DB8:0::0",
		IpAddress:       "2001:DB8::1",
		DnsServers:      []string{"001.001.001.001", "::FFFF:1.2.3.4"},
		Peers:           map[string]string{"edge": "FE80::1"},
		Primary:         &testpb.Interface{Mac: "AA:BB", Address: "::A"},
		Interfaces:      []*testpb.Interface{{InterfaceId: upperUUID}},
		NamedInterfaces: map[string]*testpb.Interface{"lan": {Address: "192.168.000.001"}},
		BackupIpv4:      new("192.000.2.2"),
		Endpoint:        &testpb.Host_EndpointIpv6{EndpointIpv6: "0:0::2"},
		DisplayName:     "Host",
	}

	require.NoError(t, fieldinfo.Normalize(h))

	want := &testpb.Host{
		Uid:             validUUID,
		Ipv4Address:     "10.0.0.1",
		Ipv6Address:     "2001:db8::",
		IpAddress:       "2001:db8::1",
		DnsServers:      []string{"1.1.1.1", "::ffff:1.2.3.4"},
		Peers:           map[string]string{"edge": "fe80::1"},
		Primary:         &testpb.Interface{Mac: "AA:BB", Address: "::a"},
		Interfaces:      []*testpb.Interface{{InterfaceId: validUUID}},
		NamedInterfaces: map[string]*testpb.Interface{"lan": {Address: "192.168.0.1"}},
		BackupIpv4:      new("192.0.2.2"),
		Endpoint:        &testpb.Host_EndpointIpv6{EndpointIpv6: "::2"},
		DisplayName:     "Host",
	}
	assert.True(t, proto.Equal(want, h), "got %v", h)

	require.NoError(t, fieldinfo.Normalize(nil))
}

func TestNormalize_UntouchedOnError(t *testing.T) {
	t.Parallel()

	h := &testpb.Host{Uid: upperUUID, Ipv4Address: "bad"}
	before := proto.Clone(h)

	violations(t, fieldinfo.Normalize(h))
	assert.True(t, proto.Equal(before, h))
}

func TestNormalize_Dynamic(t *testing.T) {
	t.Parallel()

	md := (&testpb.Host{}).ProtoReflect().Descriptor()
	raw, err := proto.Marshal(&testpb.Host{
		Uid:             upperUUID,
		DnsServers:      []string{"001.1.1.1"},
		Peers:           map[string]string{"k": "::A"},
		Primary:         &testpb.Interface{Address: "::B"},
		Interfaces:      []*testpb.Interface{{Address: "::C"}},
		NamedInterfaces: map[string]*testpb.Interface{"n": {Address: "::D"}},
	})
	require.NoError(t, err)

	dyn := dynamicpb.NewMessage(md)
	require.NoError(t, proto.Unmarshal(raw, dyn))
	require.NoError(t, fieldinfo.Normalize(dyn))

	out, err := proto.Marshal(dyn)
	require.NoError(t, err)

	got := &testpb.Host{}
	require.NoError(t, proto.Unmarshal(out, got))
	assert.Equal(t, validUUID, got.GetUid())
	assert.Equal(t, []string{"1.1.1.1"}, got.GetDnsServers())
	assert.Equal(t, "::a", got.GetPeers()["k"])
	assert.Equal(t, "::b", got.GetPrimary().GetAddress())
	assert.Equal(t, "::c", got.GetInterfaces()[0].GetAddress())
	assert.Equal(t, "::d", got.GetNamedInterfaces()["n"].GetAddress())
}

func emailRegistry(t *testing.T) *fieldinfo.Registry {
	t.Helper()

	var r fieldinfo.Registry
	require.NoError(t, r.Register("email", fieldinfo.CustomFormat{
		Validate: func(s string) error {
			a, err := mail.ParseAddress(s)
			if err != nil {
				return err
			}

			if a.Name != "" || a.Address != s {
				return errors.New("display names are not accepted")
			}

			return nil
		},
		Normalize: strings.ToLower,
		Reason:    "must be an email address",
	}))
	require.NoError(t, r.Assign(emailField, "email"))
	require.NoError(t, r.Register("live", fieldinfo.CustomFormat{Validate: func(s string) error {
		if strings.Contains(s, "dead") {
			return errors.New("dead address")
		}

		return nil
	}}))
	require.NoError(t, r.Assign("fieldinfotest.v1.Interface.address", "live"))

	return &r
}

func TestCustomFormat(t *testing.T) {
	t.Parallel()

	reg := emailRegistry(t)
	c := fieldinfo.NewChecker(fieldinfo.WithRegistry(reg))

	h := validHost()
	h.ContactEmail = "Ops <ops@example.com>"
	h.Primary.Address = "::DEAD"

	vs := violations(t, c.Validate(h))
	assert.Equal(t, []fieldinfo.Violation{
		{Path: "primary.address", Format: "live", Reason: "must be a valid live"},
		{Path: "contact_email", Format: "email", Reason: "must be an email address"},
	}, vs, "declaration order; the custom format sees the normalized \"::dead\"")

	// The AIP-202 format is checked first; the custom one runs on its
	// normalized value.
	h = &testpb.Host{ContactEmail: "Ops@Example.com", Primary: &testpb.Interface{Address: "::A"}}
	require.NoError(t, c.Normalize(h))
	assert.Equal(t, "ops@example.com", h.GetContactEmail())
	assert.Equal(t, "::a", h.GetPrimary().GetAddress(), "the custom format has no normalizer")

	h = &testpb.Host{Primary: &testpb.Interface{Address: "bad"}}
	vs = violations(t, c.Validate(h))
	assert.Equal(t, "IPV4_OR_IPV6", vs[0].Format)

	// The registry is copied: later changes do not reach the checker.
	require.NoError(t, reg.Register("late", fieldinfo.CustomFormat{Validate: func(string) error { return errors.New("x") }}))
	require.NoError(t, reg.Assign("fieldinfotest.v1.Plain.name", "late"))
	assert.False(t, c.HasFormats((&testpb.Plain{}).ProtoReflect().Descriptor()))
	assert.True(t, fieldinfo.NewChecker(fieldinfo.WithRegistry(reg)).HasFormats((&testpb.Plain{}).ProtoReflect().Descriptor()))
}

func TestRegistryErrors(t *testing.T) {
	t.Parallel()

	ok := fieldinfo.CustomFormat{Validate: func(string) error { return nil }}

	var r fieldinfo.Registry
	require.ErrorIs(t, r.Assign(emailField, "email"), fieldinfo.ErrInvalidRegistry)
	require.ErrorIs(t, r.Register("", ok), fieldinfo.ErrInvalidRegistry)
	require.ErrorIs(t, r.Register("email", fieldinfo.CustomFormat{}), fieldinfo.ErrInvalidRegistry)
	require.NoError(t, r.Register("email", ok))
	require.ErrorIs(t, r.Register("email", ok), fieldinfo.ErrInvalidRegistry)
	require.NoError(t, r.Assign(emailField, "email"))
	require.ErrorIs(t, r.Assign(emailField, "email"), fieldinfo.ErrInvalidRegistry)
}

func TestCheckField(t *testing.T) {
	t.Parallel()

	c := fieldinfo.NewChecker()
	h := &testpb.Host{Uid: "bad", DnsServers: []string{"x"}, RawAddress: []byte("x"), DisplayName: "x"}
	m := h.ProtoReflect()
	fields := m.Descriptor().Fields()

	assert.Equal(t, []fieldinfo.Violation{{Path: "a.uid", Format: "UUID4", Reason: fieldinfo.ReasonUUID4}},
		c.CheckField(m, fields.ByName("uid"), "a.uid"))
	assert.Equal(t, []string{"dns.0"}, paths(c.CheckField(m, fields.ByName("dns_servers"), "dns")))
	assert.Nil(t, c.CheckField(m, fields.ByName("raw_address"), "raw"), "a format on bytes is ignored")
	assert.Nil(t, c.CheckField(m, fields.ByName("display_name"), "d"), "no format")
	assert.Nil(t, c.CheckField(m, fields.ByName("ipv4_address"), "i"), "unset")
	assert.Nil(t, c.CheckField(nil, fields.ByName("uid"), "uid"))
	assert.Nil(t, c.CheckField((*testpb.Host)(nil).ProtoReflect(), fields.ByName("uid"), "uid"))
}

func TestHasFormats(t *testing.T) {
	t.Parallel()

	c := fieldinfo.NewChecker()
	assert.True(t, c.HasFormats((&testpb.Host{}).ProtoReflect().Descriptor()))
	assert.True(t, c.HasFormats((&testpb.Interface{}).ProtoReflect().Descriptor()))
	assert.False(t, c.HasFormats((&testpb.Plain{}).ProtoReflect().Descriptor()))
	assert.False(t, c.HasFormats(nil))

	// Cached answers stay stable.
	assert.False(t, c.HasFormats((&testpb.Plain{}).ProtoReflect().Descriptor()))
}

func TestLookup(t *testing.T) {
	t.Parallel()

	fields := (&testpb.Host{}).ProtoReflect().Descriptor().Fields()

	tests := []struct {
		field string
		want  fieldinfo.Info
	}{
		{"uid", fieldinfo.Info{Format: fieldinfo.UUID4}},
		{"ipv4_address", fieldinfo.Info{Format: fieldinfo.IPv4}},
		{"ipv6_address", fieldinfo.Info{Format: fieldinfo.IPv6}},
		{"ip_address", fieldinfo.Info{Format: fieldinfo.IPv4OrIPv6}},
		{"peers", fieldinfo.Info{Format: fieldinfo.IPv6}},
		{"raw_address", fieldinfo.Info{Format: fieldinfo.IPv4}},
		{"display_name", fieldinfo.Info{}},
		{"payload", fieldinfo.Info{ReferencedTypes: []string{"Interface", "*"}}},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()

			fd := fields.ByName(protoreflect.Name(tt.field))
			assert.Equal(t, tt.want, fieldinfo.Lookup(fd))
			assert.Equal(t, tt.want.Format, fieldinfo.FormatOf(fd))
			assert.Equal(t, tt.want, fieldinfo.Lookup(fd), "cached lookup")
		})
	}

	got := fieldinfo.Lookup(fields.ByName("payload"))
	got.ReferencedTypes[0] = "mutated"
	assert.Equal(t, "Interface", fieldinfo.Lookup(fields.ByName("payload")).ReferencedTypes[0], "an owned copy")

	assert.Equal(t, fieldinfo.Info{}, fieldinfo.Lookup(nil))
}
