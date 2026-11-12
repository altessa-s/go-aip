// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package etag

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	coreetag "github.com/altessa-s/go-atlas-core/encoding/etag"
)

// Errors specific to protobuf etag fields.
var (
	// ErrNilMessage reports a nil proto message.
	ErrNilMessage = errors.New("etag: nil message")
	// ErrNoField reports a message without a singular string field named
	// etag, or a hash path that does not name a field.
	ErrNoField = errors.New("etag: no such field")
)

// Parse decodes the etag a client sends (AIP-154, RFC 9110 §8.8.3):
//
//   - `"value"` is a strong tag and `W/"value"` a weak one; the `W/` prefix
//     is case-sensitive.
//   - A bare value without quotes (`value`) is accepted as a strong tag,
//     since AIP-154 examples and many clients send plain strings. It must
//     consist of entity-tag characters only (%x21 / %x23-7E / %x80-FF) and
//     not start with `W/`. [WithStrictQuotes] refuses bare values.
//
// Surrounding white space is trimmed. The empty string is
// [coreetag.ErrMissing]; anything else that is not an entity tag, the empty opaque
// tag `""` and a value longer than [WithMaxLength] bytes are [coreetag.ErrInvalidTag].
func Parse(s string, opts ...Option) (coreetag.Tag, error) {
	return newOptions(opts...).parse(s)
}

func (o *options) parse(s string) (coreetag.Tag, error) {
	if s == "" {
		return coreetag.Tag{}, coreetag.ErrMissing
	}

	if o.maxLength > 0 && len(s) > o.maxLength {
		return coreetag.Tag{}, fmt.Errorf("etag: longer than %d bytes: %w", o.maxLength, coreetag.ErrInvalidTag)
	}

	t := strings.TrimSpace(s)
	if t == "" {
		return coreetag.Tag{}, fmt.Errorf("etag: blank: %w", coreetag.ErrInvalidTag)
	}

	if t[0] == '"' || strings.HasPrefix(t, "W/") {
		tag, err := coreetag.Parse(t)
		if err != nil {
			return coreetag.Tag{}, fmt.Errorf("etag: not an entity tag: %w", coreetag.ErrInvalidTag)
		}

		return tag, nil
	}

	if o.strictQuotes {
		return coreetag.Tag{}, fmt.Errorf("etag: not quoted: %w", coreetag.ErrInvalidTag)
	}

	for i := range len(t) {
		if !isTagChar(t[i]) {
			return coreetag.Tag{}, fmt.Errorf("etag: invalid character at offset %d: %w", i, coreetag.ErrInvalidTag)
		}
	}

	return coreetag.Strong(t), nil
}

// isTagChar reports whether c is an RFC 9110 etagc byte: %x21 / %x23-7E /
// obs-text (%x80-FF).
func isTagChar(c byte) bool {
	return c == 0x21 || (c >= 0x23 && c <= 0x7E) || c >= 0x80
}

// FromRevision returns the weak tag W/"<rev>" of a stored resource revision
// in canonical decimal, the go-atlas-core version tag. It is weak because
// equal revisions mean semantically, not byte-for-byte, equal
// representations. A negative revision is the zero (absent) coreetag.Tag.
func FromRevision[T int64 | uint64](rev T) coreetag.Tag {
	switch v := any(rev).(type) {
	case int64:
		return coreetag.FromVersion(v)
	case uint64:
		return coreetag.Weak(strconv.FormatUint(v, 10))
	default:
		return coreetag.Tag{}
	}
}

// Revision returns the revision a [FromRevision] tag carries. It fails with
// [coreetag.ErrNotVersion] unless t is weak and its value is the canonical decimal
// (no sign, no leading zero except "0") of a revision T can hold, so
// exactly one tag maps to each revision.
func Revision[T int64 | uint64](t coreetag.Tag) (T, error) {
	v := t.Value()
	if !t.IsWeak() || !canonicalDecimal(v) {
		return 0, coreetag.ErrNotVersion
	}

	u, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, coreetag.ErrNotVersion
	}

	if _, signed := any(T(0)).(int64); signed && u > math.MaxInt64 {
		return 0, coreetag.ErrNotVersion
	}

	return T(u), nil
}

// canonicalDecimal reports whether s is "0" or decimal digits without a
// leading zero.
func canonicalDecimal(s string) bool {
	if s == "" || (s[0] == '0' && len(s) > 1) {
		return false
	}

	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}

	return true
}
