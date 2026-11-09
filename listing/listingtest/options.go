// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listingtest

import (
	"testing"

	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

//go:generate optgen generate

// SignerFactory constructs an independent signer for each conformance scenario.
type SignerFactory func(testing.TB) pagetoken.Signer

func defaultSigner(tb testing.TB) pagetoken.Signer { return pagetokentest.Codec(tb) }

type options struct {
	// signerFactory replaces the token service without weakening the mandatory cases.
	signerFactory SignerFactory `optgen:"default=defaultSigner,notnil"`
	// schemaOptions configure fixture parsers and policies before scenario-specific settings.
	schemaOptions []listing.ProfileOption `optgen:"append" optval:"clone=eager"`
}
