// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldinfo_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/fieldinfo"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestNormalizeValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		format fieldinfo.Format
		in     string
		want   string
		ok     bool
	}{
		{"uuid lowercase", fieldinfo.UUID4, "f47ac10b-58cc-4372-a567-0e02b2c3d479", "f47ac10b-58cc-4372-a567-0e02b2c3d479", true},
		{"uuid uppercase normalized", fieldinfo.UUID4, "F47AC10B-58CC-4372-A567-0E02B2C3D479", "f47ac10b-58cc-4372-a567-0e02b2c3d479", true},
		{"uuid variant 8", fieldinfo.UUID4, "f47ac10b-58cc-4372-8567-0e02b2c3d479", "f47ac10b-58cc-4372-8567-0e02b2c3d479", true},
		{"uuid variant B", fieldinfo.UUID4, "f47ac10b-58cc-4372-B567-0e02b2c3d479", "f47ac10b-58cc-4372-b567-0e02b2c3d479", true},
		{"uuid version 0 (AIP-202 example)", fieldinfo.UUID4, "F47AC10B-58CC-0372-8567-0E02B2C3D479", "", false},
		{"uuid version 7", fieldinfo.UUID4, "0190a6b4-58cc-7372-8567-0e02b2c3d479", "", false},
		{"uuid NCS variant", fieldinfo.UUID4, "f47ac10b-58cc-4372-7567-0e02b2c3d479", "", false},
		{"uuid no hyphens", fieldinfo.UUID4, "f47ac10b58cc4372a5670e02b2c3d479", "", false},
		{"uuid braces", fieldinfo.UUID4, "{f47ac10b-58cc-4372-a567-0e02b2c3d479}", "", false},
		{"uuid urn", fieldinfo.UUID4, "urn:uuid:f47ac10b-58cc-4372-a567-0e02b2c3d479", "", false},
		{"uuid misplaced hyphen", fieldinfo.UUID4, "f47ac10b5-8cc-4372-a567-0e02b2c3d479", "", false},
		{"uuid non hex", fieldinfo.UUID4, "g47ac10b-58cc-4372-a567-0e02b2c3d479", "", false},
		{"uuid empty", fieldinfo.UUID4, "", "", false},

		{"ipv4", fieldinfo.IPv4, "192.0.2.1", "192.0.2.1", true},
		{"ipv4 leading zeros condensed", fieldinfo.IPv4, "001.022.233.040", "1.22.233.40", true},
		{"ipv4 zero", fieldinfo.IPv4, "0.0.0.0", "0.0.0.0", true},
		{"ipv4 max", fieldinfo.IPv4, "255.255.255.255", "255.255.255.255", true},
		{"ipv4 octet over 255", fieldinfo.IPv4, "256.1.1.1", "", false},
		{"ipv4 four digits", fieldinfo.IPv4, "0001.1.1.1", "", false},
		{"ipv4 three octets", fieldinfo.IPv4, "1.1.1", "", false},
		{"ipv4 five octets", fieldinfo.IPv4, "1.1.1.1.1", "", false},
		{"ipv4 empty octet", fieldinfo.IPv4, "1..1.1", "", false},
		{"ipv4 trailing dot", fieldinfo.IPv4, "1.1.1.1.", "", false},
		{"ipv4 sign", fieldinfo.IPv4, "+1.1.1.1", "", false},
		{"ipv4 hex", fieldinfo.IPv4, "0x1.1.1.1", "", false},
		{"ipv4 space", fieldinfo.IPv4, " 1.1.1.1", "", false},
		{"ipv4 ipv6 value", fieldinfo.IPv4, "::1", "", false},
		{"ipv4 too long", fieldinfo.IPv4, "1.1.1.1" + strings.Repeat("0", 20), "", false},
		{"ipv4 empty", fieldinfo.IPv4, "", "", false},

		{"ipv6 compressed", fieldinfo.IPv6, "2001:0DB8:0::0", "2001:db8::", true},
		{"ipv6 full", fieldinfo.IPv6, "2001:0db8:0000:0000:0000:ff00:0042:8329", "2001:db8::ff00:42:8329", true},
		{"ipv6 loopback", fieldinfo.IPv6, "::1", "::1", true},
		{"ipv6 unspecified", fieldinfo.IPv6, "::", "::", true},
		{"ipv6 mapped ipv4", fieldinfo.IPv6, "::FFFF:192.0.2.1", "::ffff:192.0.2.1", true},
		{"ipv6 embedded ipv4", fieldinfo.IPv6, "64:ff9b::192.0.2.33", "64:ff9b::c000:221", true},
		{"ipv6 longest run compressed", fieldinfo.IPv6, "2001:db8:0:0:1:0:0:1", "2001:db8::1:0:0:1", true},
		{"ipv6 zone", fieldinfo.IPv6, "fe80::1%eth0", "", false},
		{"ipv6 brackets", fieldinfo.IPv6, "[::1]", "", false},
		{"ipv6 ipv4 value", fieldinfo.IPv6, "192.0.2.1", "", false},
		{"ipv6 two compressions", fieldinfo.IPv6, "2001::db8::1", "", false},
		{"ipv6 five digit group", fieldinfo.IPv6, "2001:00db8::1", "", false},
		{"ipv6 nine groups", fieldinfo.IPv6, "1:2:3:4:5:6:7:8:9", "", false},
		{"ipv6 embedded ipv4 leading zero", fieldinfo.IPv6, "::ffff:192.0.2.01", "", false},
		{"ipv6 too long", fieldinfo.IPv6, strings.Repeat("0", 50) + "::1", "", false},
		{"ipv6 empty", fieldinfo.IPv6, "", "", false},

		{"either ipv4", fieldinfo.IPv4OrIPv6, "010.0.0.1", "10.0.0.1", true},
		{"either ipv6", fieldinfo.IPv4OrIPv6, "2001:DB8::1", "2001:db8::1", true},
		{"either invalid", fieldinfo.IPv4OrIPv6, "example.com", "", false},
		{"either invalid ipv6", fieldinfo.IPv4OrIPv6, "fe80::1%1", "", false},

		{"unspecified passthrough", fieldinfo.Unspecified, "anything", "anything", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := fieldinfo.NormalizeValue(tt.format, tt.in)
			verr := fieldinfo.ValidateValue(tt.format, tt.in)

			if !tt.ok {
				require.Error(t, err)
				require.ErrorIs(t, err, fieldinfo.ErrInvalidValue)
				require.ErrorIs(t, err, coreerrs.ErrInvalidArgument)
				require.ErrorIs(t, verr, fieldinfo.ErrInvalidValue)

				ve, ok := errors.AsType[*fieldinfo.ValueError](err)
				require.True(t, ok)
				assert.Equal(t, tt.format.String(), ve.Format)

				if tt.in != "" {
					assert.NotContains(t, ve.Error(), tt.in, "the error must not echo the value")
				}

				return
			}

			require.NoError(t, err)
			require.NoError(t, verr)
			assert.Equal(t, tt.want, got)

			again, err := fieldinfo.NormalizeValue(tt.format, got)
			require.NoError(t, err)
			assert.Equal(t, got, again, "normalization is idempotent")
		})
	}
}

func TestValueErrorText(t *testing.T) {
	t.Parallel()

	err := fieldinfo.ValidateValue(fieldinfo.IPv4OrIPv6, "x")
	require.EqualError(t, err, "fieldinfo: IPV4_OR_IPV6: "+fieldinfo.ReasonIPv4OrIPv6)
}

func TestUnknownFormat(t *testing.T) {
	t.Parallel()

	const future = fieldinfo.Format(99)

	assert.False(t, fieldinfo.Known(future))
	assert.False(t, fieldinfo.Known(fieldinfo.Unspecified))
	assert.True(t, fieldinfo.Known(fieldinfo.IPv4OrIPv6))

	require.ErrorIs(t, fieldinfo.ValidateValue(future, "x"), fieldinfo.ErrUnknownFormat)

	_, err := fieldinfo.Equal(future, "x", "x")
	require.ErrorIs(t, err, fieldinfo.ErrUnknownFormat)
}

func TestEqual(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		format fieldinfo.Format
		a, b   string
		want   bool
	}{
		{"uuid case", fieldinfo.UUID4, "F47AC10B-58CC-4372-A567-0E02B2C3D479", "f47ac10b-58cc-4372-a567-0e02b2c3d479", true},
		{"uuid different", fieldinfo.UUID4, "f47ac10b-58cc-4372-a567-0e02b2c3d479", "f47ac10b-58cc-4372-a567-0e02b2c3d470", false},
		{"ipv4 leading zeros", fieldinfo.IPv4, "001.022.233.040", "1.22.233.40", true},
		{"ipv6 compression", fieldinfo.IPv6, "2001:0DB8:0::0", "2001:db8::", true},
		{"ipv4 vs mapped", fieldinfo.IPv4OrIPv6, "192.0.2.1", "::ffff:192.0.2.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := fieldinfo.Equal(tt.format, tt.a, tt.b)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	_, err := fieldinfo.Equal(fieldinfo.IPv4, "1.1.1.1", "bad")
	require.ErrorIs(t, err, fieldinfo.ErrInvalidValue)

	_, err = fieldinfo.Equal(fieldinfo.IPv4, "bad", "1.1.1.1")
	require.ErrorIs(t, err, fieldinfo.ErrInvalidValue)
}

func FuzzNormalizeValue(f *testing.F) {
	for _, s := range []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479", "001.022.233.040", "2001:0DB8:0::0", "::ffff:1.2.3.4", "fe80::1%eth0", ""} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		for _, format := range []fieldinfo.Format{fieldinfo.UUID4, fieldinfo.IPv4, fieldinfo.IPv6, fieldinfo.IPv4OrIPv6} {
			n, err := fieldinfo.NormalizeValue(format, s)
			if err != nil {
				continue
			}

			again, err := fieldinfo.NormalizeValue(format, n)
			if err != nil || again != n {
				t.Fatalf("%v: normalized %q to %q, which normalizes to %q (%v)", format, s, n, again, err)
			}
		}
	})
}
