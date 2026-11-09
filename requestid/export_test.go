// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package requestid

import "google.golang.org/protobuf/proto"

// Fingerprint exposes the request fingerprint to the black-box tests.
func Fingerprint(g *Guard, req proto.Message) ([]byte, error) {
	m := req.ProtoReflect()

	fd, err := g.field(m.Descriptor())
	if err != nil {
		return nil, err
	}

	return g.fingerprint(m, fd)
}

// StoreKey exposes the store key derivation to the black-box tests.
func StoreKey(scope Scope, id string) string { return storeKey(scope, id) }
