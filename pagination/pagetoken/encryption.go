// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/altessa-s/go-kryptos/sealbox"
)

// An encrypted token (version 2) is
//
//	version | key id | nonce | AES-256-GCM(content) with its tag
//
// sealed by go-kryptos sealbox. The version and the key id are the associated
// data, so neither can be swapped. Encryption keys are derived from the
// signing keys with HKDF-SHA256, so the signing key and [WithPreviousKeys]
// rotate both formats at once and every codec opens encrypted tokens, whether
// or not it issues them.
const (
	versionEncrypted byte = 2
	// keyIDLength is the raw length of a sealbox key id (hex in sealbox).
	keyIDLength = sealbox.KeyIDLen / 2
	nonceLength = 12 // AES-GCM standard nonce
	tagLength   = 16 // AES-GCM tag
	// aadLength covers the version and the key id.
	aadLength = 1 + keyIDLength
	// encryptedOverhead is what an encrypted token adds to its content
	// besides the version byte.
	encryptedOverhead = keyIDLength + nonceLength + tagLength
)

// encryptionInfo separates the derived encryption keys from any other use of
// a signing key. It is part of the token wire format, not a package path: changing it re-keys every encrypted token, so it keeps the
// value it had when the package lived at data/keyset.
const encryptionInfo = "go-atlas data/keyset v2 page-token encryption"

// sealer encrypts and decrypts token content with keys derived from the
// signing keys.
type sealer struct {
	box   *sealbox.Box
	keyID [keyIDLength]byte // of the current key
}

// newSealer derives an encryption key from current and from each previous
// signing key.
func newSealer(current []byte, previous [][]byte) (*sealer, error) {
	encoded := make([]string, 0, 1+len(previous))

	var currentKey []byte

	for i, k := range append([][]byte{current}, previous...) {
		derived, err := hkdf.Key(sha256.New, k, nil, encryptionInfo, sealbox.KeyLen)
		if err != nil {
			return nil, fmt.Errorf("pagetoken: derive encryption key: %w", err)
		}

		if i == 0 {
			currentKey = derived
		}

		encoded = append(encoded, base64.StdEncoding.EncodeToString(derived))
	}

	box, err := sealbox.New(encoded[0], encoded[1:]...)
	if err != nil {
		return nil, fmt.Errorf("pagetoken: encryption keys: %w", err)
	}

	s := &sealer{box: box}
	if _, err = hex.Decode(s.keyID[:], []byte(sealbox.KeyID(currentKey))); err != nil {
		return nil, fmt.Errorf("pagetoken: encryption key id: %w", err)
	}

	return s, nil
}

// issueEncrypted returns an encrypted token carrying payload, bound to b. buf
// is scratch space; the content written to it is cleared before it returns.
func (c *Codec) issueEncrypted(buf []byte, payload []byte, b Bindings) (string, error) {
	aad := buf[:0]
	aad = append(aad, versionEncrypted)
	aad = append(aad, c.sealer.keyID[:]...)
	content := c.appendContent(buf[aadLength:aadLength], payload, b)
	sealed, err := c.sealer.box.Seal(content, aad)
	clear(content)

	if err != nil {
		return "", fmt.Errorf("pagetoken: encrypt page token: %w", err)
	}

	raw := buf[:aadLength]
	raw = append(raw, sealed.Nonce...)
	raw = append(raw, sealed.Ciphertext...)

	return encodeToken(raw), nil
}

// open decrypts and authenticates an encrypted token and returns its
// content. Every failure — truncated, tampered, an unknown key — is
// [ErrInvalidToken].
func (s *sealer) open(raw []byte) ([]byte, error) {
	if len(raw) < 1+encryptedOverhead+minContentLength {
		return nil, ErrInvalidToken
	}

	sealed := sealbox.Sealed{
		KeyID:      hex.EncodeToString(raw[1:aadLength]),
		Nonce:      raw[aadLength : aadLength+nonceLength],
		Ciphertext: raw[aadLength+nonceLength:],
	}

	content, err := s.box.Open(sealed, raw[:aadLength])
	if err != nil {
		return nil, ErrInvalidToken
	}

	return content, nil
}
