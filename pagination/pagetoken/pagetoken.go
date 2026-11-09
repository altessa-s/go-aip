// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"strings"
	"sync"
	"time"
)

// Errors returned by [Codec.Resolve] and [New].
var (
	// ErrInvalidToken is returned for a malformed token or a bad signature.
	ErrInvalidToken = errors.New("pagetoken: invalid page token")
	// ErrExpiredToken is returned for a token older than the configured TTL.
	ErrExpiredToken = errors.New("pagetoken: page token expired")
	// ErrSortChanged is returned when the query's sort differs from the one
	// the token was issued for.
	ErrSortChanged = errors.New("pagetoken: sort changed since the page token was issued")
	// ErrFilterChanged is returned when the query's filter differs from the
	// one the token was issued for. Resuming under another filter could reach
	// data the original query excluded, so it is rejected.
	ErrFilterChanged = errors.New("pagetoken: filter changed since the page token was issued")
	// ErrSubjectMismatch is returned when a token issued to one principal is
	// presented by another.
	ErrSubjectMismatch = errors.New("pagetoken: page token issued to another subject")
	// ErrPayloadTooLarge is returned by [Codec.Issue] for a payload longer
	// than [MaxPayloadLength].
	ErrPayloadTooLarge = errors.New("pagetoken: payload too large")
	// ErrWeakKey is returned by [New] for a key shorter than [MinKeyLength].
	ErrWeakKey = errors.New("pagetoken: signing key too short")
	// ErrUnencryptedToken is returned by [Codec.Resolve] under
	// [WithStrictEncryption] for a signed token that is not encrypted. It
	// wraps [ErrInvalidToken].
	ErrUnencryptedToken = fmt.Errorf("%w: not encrypted", ErrInvalidToken)
)

// Bindings tie a token to the query it was issued for. Each value is an
// opaque fingerprint chosen by the caller; [Codec.Resolve] rejects a token
// whose bindings differ.
type Bindings struct {
	// Sort fingerprints the ordering.
	Sort string
	// Filter fingerprints the predicate, excluding the page position and size.
	Filter string
	// Subject identifies the principal the token was issued to.
	Subject string
}

// A token is a version byte followed by its content — issue time, the three
// binding digests, the payload length and the payload — either in clear and
// signed (version 1) or encrypted (version 2, see encryption.go).
const (
	versionSigned byte = 1
	digestLength       = 16 // truncated SHA-256 of a binding
	macLength          = sha256.Size
	// contentHeaderLength covers the issue time and the three binding digests.
	contentHeaderLength = 8 + 3*digestLength
	// minContentLength adds a one-byte payload length.
	minContentLength = contentHeaderLength + 1
)

// tokenEncoding is strict so that every token has exactly one encoding: the
// unused bits of a final character must be zero.
var tokenEncoding = base64.RawURLEncoding.Strict()

// Codec issues and resolves signed, stateless page tokens. A token carries a
// position payload, its issue time and digests of its [Bindings], signed with
// HMAC-SHA256 or, under [WithEncryption], encrypted with AES-256-GCM. It is
// safe for concurrent use.
type Codec struct {
	key            *signer
	previous       []*signer
	sealer         *sealer
	opts           *options
	rawBuffers     sync.Pool
	maxTokenLength int
}

// New returns a Codec signing with key, which must be at least
// [MinKeyLength] bytes and the same on every replica that resolves the
// tokens.
func New(key []byte, opts ...Option) (*Codec, error) {
	if len(key) < MinKeyLength {
		return nil, fmt.Errorf("%w: %d bytes, minimum %d", ErrWeakKey, len(key), MinKeyLength)
	}

	o := newOptions(opts...)
	if o.maxPayloadLength > int(^uint(0)>>1)/4 {
		return nil, fmt.Errorf("%w: configured limit overflows token size", ErrPayloadTooLarge)
	}
	// Keys are copied so later changes to the caller's buffers cannot alter
	// which tokens verify.
	previous := make([]*signer, len(o.previousKeys))
	for i, k := range o.previousKeys {
		if len(k) < MinKeyLength {
			return nil, fmt.Errorf("%w: previous key of %d bytes, minimum %d", ErrWeakKey, len(k), MinKeyLength)
		}

		previous[i] = newSigner(k)
	}

	s, err := newSealer(key, o.previousKeys)
	if err != nil {
		return nil, err
	}

	rawLength := 1 + max(macLength, encryptedOverhead) + contentHeaderLength + binary.MaxVarintLen64 + o.maxPayloadLength
	c := &Codec{key: newSigner(key), previous: previous, sealer: s, opts: o, maxTokenLength: tokenEncoding.EncodedLen(rawLength)}
	c.rawBuffers.New = func() any {
		b := make([]byte, min(rawLength, 1+max(macLength, encryptedOverhead)+contentHeaderLength+binary.MaxVarintLen64+MaxPayloadLength))
		return &b
	}

	return c, nil
}

// Issue returns a token carrying payload, bound to b.
func (c *Codec) Issue(payload []byte, b Bindings) (string, error) {
	if len(payload) > c.opts.maxPayloadLength {
		return "", fmt.Errorf("%w: %d bytes, maximum %d", ErrPayloadTooLarge, len(payload), c.opts.maxPayloadLength)
	}

	scratch := c.rawBuffers.Get().(*[]byte) //nolint:errcheck // pool holds only this type
	defer c.rawBuffers.Put(scratch)

	buf := *scratch

	if c.opts.encryption || c.opts.strictEncryption {
		return c.issueEncrypted(buf, payload, b)
	}

	body := buf[:0]
	body = append(body, versionSigned)
	body = c.appendContent(body, payload, b)
	body = c.key.appendMAC(body, body)

	return encodeToken(body), nil
}

// appendContent appends the content of a token to dst: issue time, binding
// digests, payload length and payload.
func (c *Codec) appendContent(dst, payload []byte, b Bindings) []byte {
	dst = binary.BigEndian.AppendUint64(dst, uint64(c.opts.clock().UnixMilli())) //nolint:gosec // epoch milliseconds are positive
	dst = appendDigests(dst, b)
	dst = binary.AppendUvarint(dst, uint64(len(payload)))

	return append(dst, payload...)
}

// Resolve verifies token and returns its payload. A signed token's signature
// is checked, or an encrypted token is decrypted and authenticated, first;
// only then are expiry and the bindings compared, so a forged token never
// reveals which binding it would have failed. Both versions are accepted,
// except a signed token under [WithStrictEncryption] ([ErrUnencryptedToken]).
func (c *Codec) Resolve(token string, b Bindings) ([]byte, error) {
	if token == "" || len(token) > c.maxTokenLength || !canonicalAlphabet(token) {
		return nil, ErrInvalidToken
	}

	scratch := c.rawBuffers.Get().(*[]byte) //nolint:errcheck // pool holds only this type
	defer c.rawBuffers.Put(scratch)

	buf := *scratch

	if needed := tokenEncoding.DecodedLen(len(token)); needed > len(buf) {
		buf = make([]byte, needed)
	}

	rawLength, err := decodeToken(buf, token)
	if err != nil || rawLength == 0 {
		return nil, ErrInvalidToken
	}

	raw := buf[:rawLength]

	switch raw[0] {
	case versionSigned:
		if c.opts.strictEncryption {
			return nil, ErrUnencryptedToken
		}

		if len(raw) < 1+minContentLength+macLength {
			return nil, ErrInvalidToken
		}

		body, mac := raw[:len(raw)-macLength], raw[len(raw)-macLength:]
		if !c.verify(body, mac) {
			return nil, ErrInvalidToken
		}

		payload, err := c.check(body[1:], b)
		if err != nil {
			return nil, err
		}

		return bytes.Clone(payload), nil
	case versionEncrypted:
		content, err := c.sealer.open(raw)
		if err != nil {
			return nil, err
		}

		return c.check(content, b)
	default:
		return nil, ErrInvalidToken
	}
}

// check validates the authenticated content of a token — issue time, then
// bindings, then payload length — and returns the payload, a subslice of
// content.
func (c *Codec) check(content []byte, b Bindings) ([]byte, error) {
	if len(content) < minContentLength {
		return nil, ErrInvalidToken
	}

	issued := time.UnixMilli(int64(binary.BigEndian.Uint64(content[:8]))) //nolint:gosec // authenticated by us

	now := c.opts.clock()
	if issued.After(now.Add(c.opts.maxClockSkew)) {
		return nil, ErrInvalidToken
	}

	if !c.opts.expiryDisabled && now.Sub(issued) > c.opts.ttl {
		return nil, ErrExpiredToken
	}

	digests := content[8:contentHeaderLength]

	sortDigest, filterDigest, subjectDigest := digest(b.Sort), digest(b.Filter), digest(b.Subject)
	switch {
	case !hmac.Equal(digests[:digestLength], sortDigest[:]):
		return nil, ErrSortChanged
	case !hmac.Equal(digests[digestLength:2*digestLength], filterDigest[:]):
		return nil, ErrFilterChanged
	case !hmac.Equal(digests[2*digestLength:], subjectDigest[:]):
		return nil, ErrSubjectMismatch
	}

	n, size := binary.Uvarint(content[contentHeaderLength:])

	rest := content[contentHeaderLength+max(size, 0):]
	if size <= 0 || n > uint64(c.opts.maxPayloadLength) || n != uint64(len(rest)) {
		return nil, ErrInvalidToken
	}

	return rest, nil
}

// canonicalAlphabet reports whether token uses only the URL-safe base64
// alphabet. The decoder skips CR and LF, which would otherwise let several
// strings decode to the same token.
func canonicalAlphabet(token string) bool {
	for i := range len(token) {
		switch c := token[i]; {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}

	return true
}

// verify reports whether mac signs body under the current or a previous key.
func (c *Codec) verify(body, mac []byte) bool {
	if c.key.verify(body, mac) {
		return true
	}

	for _, k := range c.previous {
		if k.verify(body, mac) {
			return true
		}
	}

	return false
}

// signer signs with one immutable key. HMAC states are pooled; each is
// checked out by a single caller and Reset before use.
type signer struct {
	pool sync.Pool
}

type macState struct {
	h   hash.Hash
	sum [macLength]byte
}

func newSigner(key []byte) *signer {
	// The key is copied so later changes to the caller's buffer cannot alter
	// which tokens verify.
	key = bytes.Clone(key)

	return &signer{pool: sync.Pool{New: func() any {
		return &macState{h: hmac.New(sha256.New, key)}
	}}}
}

// appendMAC appends the MAC of body to dst.
func (s *signer) appendMAC(dst, body []byte) []byte {
	st := s.pool.Get().(*macState) //nolint:errcheck // pool holds only this type
	defer s.pool.Put(st)

	st.h.Reset()
	st.h.Write(body)

	return append(dst, st.h.Sum(st.sum[:0])...)
}

// verify reports in constant time whether mac is the MAC of body.
func (s *signer) verify(body, mac []byte) bool {
	st := s.pool.Get().(*macState) //nolint:errcheck // pool holds only this type
	defer s.pool.Put(st)

	st.h.Reset()
	st.h.Write(body)

	return hmac.Equal(mac, st.h.Sum(st.sum[:0]))
}

// codecChunk is the number of encoded characters handled per step by
// encodeToken and decodeToken. It is a multiple of 4, so every chunk but the
// last holds whole base64 quanta and chunked coding equals one-shot coding.
const (
	codecChunk = 256
	// rawChunk is the number of raw bytes encoded into one codecChunk.
	rawChunk = codecChunk / 4 * 3
)

// encodeToken returns the encoding of raw, building the string in place.
func encodeToken(raw []byte) string {
	var sb strings.Builder
	sb.Grow(tokenEncoding.EncodedLen(len(raw)))

	var chunk [codecChunk]byte

	for len(raw) > 0 {
		n := min(len(raw), rawChunk)
		tokenEncoding.Encode(chunk[:], raw[:n])
		sb.Write(chunk[:tokenEncoding.EncodedLen(n)])
		raw = raw[n:]
	}

	return sb.String()
}

// decodeToken decodes token into dst without converting it to a byte slice.
// dst must hold the decoded length.
func decodeToken(dst []byte, token string) (int, error) {
	var chunk [codecChunk]byte

	n := 0

	for len(token) > 0 {
		k := copy(chunk[:], token)

		m, err := tokenEncoding.Decode(dst[n:], chunk[:k])
		if err != nil {
			return 0, err
		}

		n += m
		token = token[k:]
	}

	return n, nil
}

func appendDigests(dst []byte, b Bindings) []byte {
	sortDigest, filterDigest, subjectDigest := digest(b.Sort), digest(b.Filter), digest(b.Subject)
	dst = append(dst, sortDigest[:]...)
	dst = append(dst, filterDigest[:]...)

	return append(dst, subjectDigest[:]...)
}

// digest returns a truncated SHA-256 of a binding.
func digest(s string) [digestLength]byte {
	sum := sha256.Sum256([]byte(s))
	return [digestLength]byte(sum[:digestLength])
}
