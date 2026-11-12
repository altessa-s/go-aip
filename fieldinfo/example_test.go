// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldinfo_test

import (
	"errors"
	"fmt"
	"net/mail"

	"github.com/altessa-s/go-aip/fieldinfo"

	testpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldinfotest/v1"
	annotations "google.golang.org/genproto/googleapis/api/annotations"
)

func ExampleValidate() {
	host := &testpb.Host{
		Ipv4Address: "192.0.2.1",
		DnsServers:  []string{"1.1.1.1", "dns.example"},
		Primary:     &testpb.Interface{Address: "fe80::1%eth0"},
	}

	err := fieldinfo.Validate(host)
	if ve, ok := errors.AsType[*fieldinfo.ViolationError](err); ok {
		for _, v := range ve.Violations {
			fmt.Println(v.Path, v.Format)
		}
	}
	// Output:
	// dns_servers.1 IPV4_OR_IPV6
	// primary.address IPV4_OR_IPV6
}

func ExampleNormalize() {
	host := &testpb.Host{
		Uid:         "F47AC10B-58CC-4372-A567-0E02B2C3D479",
		Ipv4Address: "001.022.233.040",
		Ipv6Address: "2001:0DB8:0::0",
	}

	if err := fieldinfo.Normalize(host); err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(host.GetUid())
	fmt.Println(host.GetIpv4Address())
	fmt.Println(host.GetIpv6Address())
	// Output:
	// f47ac10b-58cc-4372-a567-0e02b2c3d479
	// 1.22.233.40
	// 2001:db8::
}

func ExampleEqual() {
	same, err := fieldinfo.Equal(annotations.FieldInfo_IPV6, "2001:0DB8:0::0", "2001:db8::")
	fmt.Println(same, err)
	// Output: true <nil>
}

func ExampleRegistry() {
	// AIP-202 has no email format: register one under a name of your choice
	// and assign it to the fields that hold email addresses.
	var reg fieldinfo.Registry
	if err := reg.Register("email", fieldinfo.CustomFormat{
		Validate: func(s string) error {
			_, err := mail.ParseAddress(s)
			return err
		},
		Reason: "must be an email address",
	}); err != nil {
		fmt.Println(err)
		return
	}

	if err := reg.Assign("fieldinfotest.v1.Host.contact_email", "email"); err != nil {
		fmt.Println(err)
		return
	}

	checker := fieldinfo.NewChecker(fieldinfo.WithRegistry(&reg))
	fmt.Println(checker.Validate(&testpb.Host{ContactEmail: "not an email"}))
	// Output: fieldinfo: 1 violation(s): contact_email (email) must be an email address
}
