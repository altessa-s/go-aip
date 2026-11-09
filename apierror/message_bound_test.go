// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package apierror_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/apierror"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestBoundMessage(t *testing.T) {
	t.Parallel()

	limit := apierror.MaxExposedMessageLength
	tests := []struct {
		name string
		in   string
		cut  bool
	}{
		{"empty", "", false},
		{"short", "invalid argument: bad cursor", false},
		{"exactly the bound in ASCII", strings.Repeat("a", limit), false},
		{"exactly the bound in multibyte runes", strings.Repeat("я", limit), false},
		{"one rune over", strings.Repeat("a", limit+1), true},
		{"multibyte runes over the bound", strings.Repeat("日本", limit), true},
		{"emoji over the bound", strings.Repeat("🙂", 3*limit), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := apierror.BoundMessage(tc.in)
			if !tc.cut {
				assert.Equal(t, tc.in, got, "text under the bound is unchanged")

				return
			}

			assert.True(t, utf8.ValidString(got), "cut on a rune boundary")
			assert.Equal(t, limit, utf8.RuneCountInString(got))
			assert.True(t, strings.HasSuffix(got, apierror.TruncatedSuffix))
			assert.True(t, strings.HasPrefix(tc.in, strings.TrimSuffix(got, apierror.TruncatedSuffix)), "a prefix of the input")
		})
	}
}

// TestMapperBoundsExposedText pins that an exposed error echoing a large client input reaches the status cut to
// MaxExposedMessageLength runes, as does the text of a FieldError, while short texts keep their exact wording.
func TestMapperBoundsExposedText(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("ж", 10*apierror.MaxExposedMessageLength)
	unknown := errors.New("db: " + huge)

	exposing, err := apierror.NewMapper(apierror.WithExposeUnknown(true))
	require.NoError(t, err)

	tests := []struct {
		name   string
		mapper *apierror.Mapper
		err    error
	}{
		{"exposed rule", nil, fmt.Errorf("%w: filter %q", coreerrs.ErrInvalidArgument, huge)},
		{"field error", nil, &coreerrs.FieldError{Field: "filter", Reason: huge, Err: coreerrs.ErrInvalidArgument}},
		{"exposed unknown error", exposing, unknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var e *apierror.Error
			if tc.mapper == nil {
				e = apierror.FromError(tc.err)
			} else {
				e = tc.mapper.Map(tc.err)
			}

			msg := e.Message()
			assert.Equal(t, apierror.MaxExposedMessageLength, utf8.RuneCountInString(msg))
			assert.True(t, strings.HasSuffix(msg, apierror.TruncatedSuffix))
			assert.True(t, utf8.ValidString(msg))

			for _, v := range e.FieldViolations() {
				assert.LessOrEqual(t, utf8.RuneCountInString(v.GetDescription()), apierror.MaxExposedMessageLength)
			}
		})
	}

	short := apierror.FromError(&coreerrs.FieldError{Field: "pageSize", Reason: "must not be negative", Err: coreerrs.ErrInvalidArgument})
	assert.Equal(t, "pageSize: must not be negative", short.Message())
	assert.Equal(t, "must not be negative", short.FieldViolations()[0].GetDescription())
}
