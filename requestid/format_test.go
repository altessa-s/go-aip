// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package requestid_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/requestid"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestCanonical(t *testing.T) {
	t.Parallel()

	const (
		v4    = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
		v4Up  = "F47AC10B-58CC-4372-A567-0E02B2C3D479"
		v7    = "01890a5d-ac96-774b-bcce-b302099a8057"
		aip   = "F47AC10B-58CC-0372-8567-0E02B2C3D479" // the AIP-202 example: version nibble 0
		ascii = "client-req_42:ok"
	)

	tests := []struct {
		name    string
		raw     string
		format  requestid.Format
		want    string
		wantErr string
	}{
		{name: "empty is no ID", raw: "", format: requestid.FormatUUID, want: ""},
		{name: "lowercase v4", raw: v4, format: requestid.FormatUUID, want: v4},
		{name: "uppercase normalized", raw: v4Up, format: requestid.FormatUUID, want: v4},
		{name: "mixed case normalized", raw: "F47ac10b-58CC-4372-a567-0e02B2c3d479", format: requestid.FormatUUID, want: v4},
		{name: "any version", raw: v7, format: requestid.FormatUUID, want: v7},
		{name: "AIP-202 example", raw: aip, format: requestid.FormatUUID, want: strings.ToLower(aip)},
		{name: "nil UUID", raw: "00000000-0000-0000-0000-000000000000", format: requestid.FormatUUID, wantErr: "Nil or Max"},
		{name: "max UUID", raw: "FFFFFFFF-ffff-ffff-ffff-ffffffffffff", format: requestid.FormatUUID, wantErr: "Nil or Max"},
		{name: "no hyphens", raw: "f47ac10b58cc4372a5670e02b2c3d479", format: requestid.FormatUUID, wantErr: "8-4-4-4-12"},
		{name: "braces", raw: "{f47ac10b-58cc-4372-a567-0e02b2c3d4}", format: requestid.FormatUUID, wantErr: "8-4-4-4-12"},
		{name: "urn", raw: "urn:uuid:f47ac10b-58cc-4372-a567-0e02", format: requestid.FormatUUID, wantErr: "more than the limit"},
		{name: "misplaced hyphen", raw: "f47ac10b5-8cc-4372-a567-0e02b2c3d479", format: requestid.FormatUUID, wantErr: "8-4-4-4-12"},
		{name: "non hex", raw: "g47ac10b-58cc-4372-a567-0e02b2c3d479", format: requestid.FormatUUID, wantErr: "8-4-4-4-12"},
		{name: "too long", raw: v4 + "0", format: requestid.FormatUUID, wantErr: "37 bytes long"},
		{name: "v4 accepted", raw: v4Up, format: requestid.FormatUUID4, want: v4},
		{name: "v4 variant 8", raw: "f47ac10b-58cc-4372-8567-0e02b2c3d479", format: requestid.FormatUUID4, want: "f47ac10b-58cc-4372-8567-0e02b2c3d479"},
		{name: "v4 variant B", raw: "f47ac10b-58cc-4372-B567-0e02b2c3d479", format: requestid.FormatUUID4, want: "f47ac10b-58cc-4372-b567-0e02b2c3d479"},
		{name: "v4 rejects v7", raw: v7, format: requestid.FormatUUID4, wantErr: "version 4"},
		{name: "v4 rejects variant c", raw: "f47ac10b-58cc-4372-c567-0e02b2c3d479", format: requestid.FormatUUID4, wantErr: "version 4"},
		{name: "v4 rejects AIP-202 example", raw: aip, format: requestid.FormatUUID4, wantErr: "version 4"},
		{name: "ascii kept as is", raw: ascii, format: requestid.FormatASCII, want: ascii},
		{name: "ascii case sensitive", raw: "ABC", format: requestid.FormatASCII, want: "ABC"},
		{name: "ascii 36", raw: strings.Repeat("x", 36), format: requestid.FormatASCII, want: strings.Repeat("x", 36)},
		{name: "ascii 37", raw: strings.Repeat("x", 37), format: requestid.FormatASCII, wantErr: "limit of 36"},
		{name: "ascii space", raw: "a b", format: requestid.FormatASCII, wantErr: "printable ASCII"},
		{name: "ascii non ascii", raw: "ключ", format: requestid.FormatASCII, wantErr: "printable ASCII"},
		{name: "ascii control", raw: "a\x00", format: requestid.FormatASCII, wantErr: "printable ASCII"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := requestid.Canonical(tc.raw, tc.format)
			if tc.wantErr != "" {
				require.ErrorIs(t, err, requestid.ErrInvalidRequestID)
				require.ErrorIs(t, err, coreerrs.ErrInvalidArgument)
				require.ErrorContains(t, err, tc.wantErr)
				assert.NotContains(t, err.Error(), tc.raw, "the error must not echo the value")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCanonicalUnknownFormat(t *testing.T) {
	t.Parallel()

	_, err := requestid.Canonical("x", requestid.Format(42))
	require.Error(t, err)
	require.False(t, errors.Is(err, requestid.ErrInvalidRequestID))
	require.ErrorContains(t, err, "Format(42)")
}

func TestFormatString(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "UUID", requestid.FormatUUID.String())
	assert.Equal(t, "UUID4", requestid.FormatUUID4.String())
	assert.Equal(t, "ASCII", requestid.FormatASCII.String())
	assert.Equal(t, "Format(-1)", requestid.Format(-1).String())
}

func FuzzCanonical(f *testing.F) {
	f.Add("f47ac10b-58cc-4372-a567-0e02b2c3d479")
	f.Add("F47AC10B-58CC-0372-8567-0E02B2C3D479")
	f.Add("")
	f.Add("abc")

	f.Fuzz(func(t *testing.T, raw string) {
		for _, format := range []requestid.Format{requestid.FormatUUID, requestid.FormatUUID4, requestid.FormatASCII} {
			got, err := requestid.Canonical(raw, format)
			if err != nil {
				require.ErrorIs(t, err, requestid.ErrInvalidRequestID)
				continue
			}

			require.LessOrEqual(t, len(got), requestid.MaxLength)

			again, err := requestid.Canonical(got, format)
			require.NoError(t, err, "a canonical ID must be valid")
			require.Equal(t, got, again, "canonicalization must be idempotent")
		}
	})
}
