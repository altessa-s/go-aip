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

// Errors of the etag package. [ErrMissing], [ErrInvalid], [ErrMismatch] and
// [ErrNotRevision] are the go-atlas-core entity-tag sentinels, so
// errors.Is matches across both packages.
var (
	// ErrMissing reports an empty etag where one is required.
	ErrMissing = coreetag.ErrMissing
	// ErrInvalid reports a value that is not an entity tag (see [Parse]).
	ErrInvalid = coreetag.ErrInvalidTag
	// ErrMismatch reports an etag that does not name the current state of
	// the resource. It wraps core/errors.ErrConflict, which the API maps
	// to ABORTED (AIP-154).
	ErrMismatch = coreetag.ErrMismatch
	// ErrNotRevision reports a tag [FromRevision] did not issue.
	ErrNotRevision = coreetag.ErrNotVersion
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
// [ErrMissing]; anything else that is not an entity tag, the empty opaque
// tag `""` and a value longer than [WithMaxLength] bytes are [ErrInvalid].
func Parse(s string, opts ...Option) (coreetag.Tag, error) {
	return newOptions(opts...).parse(s)
}

func (o *options) parse(s string) (coreetag.Tag, error) {
	if s == "" {
		return coreetag.Tag{}, ErrMissing
	}

	if o.maxLength > 0 && len(s) > o.maxLength {
		return coreetag.Tag{}, fmt.Errorf("etag: longer than %d bytes: %w", o.maxLength, ErrInvalid)
	}

	t := strings.TrimSpace(s)
	if t == "" {
		return coreetag.Tag{}, fmt.Errorf("etag: blank: %w", ErrInvalid)
	}

	if t[0] == '"' || strings.HasPrefix(t, "W/") {
		tag, err := coreetag.Parse(t)
		if err != nil {
			return coreetag.Tag{}, fmt.Errorf("etag: not an entity tag: %w", ErrInvalid)
		}

		return tag, nil
	}

	if o.strictQuotes {
		return coreetag.Tag{}, fmt.Errorf("etag: not quoted: %w", ErrInvalid)
	}

	for i := range len(t) {
		if !isTagChar(t[i]) {
			return coreetag.Tag{}, fmt.Errorf("etag: invalid character at offset %d: %w", i, ErrInvalid)
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
// [ErrNotRevision] unless t is weak and its value is the canonical decimal
// (no sign, no leading zero except "0") of a revision T can hold, so
// exactly one tag maps to each revision.
func Revision[T int64 | uint64](t coreetag.Tag) (T, error) {
	v := t.Value()
	if !t.IsWeak() || !canonicalDecimal(v) {
		return 0, ErrNotRevision
	}

	u, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, ErrNotRevision
	}

	if _, signed := any(T(0)).(int64); signed && u > math.MaxInt64 {
		return 0, ErrNotRevision
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
