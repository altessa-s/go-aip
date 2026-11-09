// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/orderby"
)

type failingFilterParser struct{ err error }

func (p failingFilterParser) Parse(context.Context, string) (filter.Node, error) { return nil, p.err }

type singleOrderParser struct{}

func (singleOrderParser) Parse(context.Context, string) (orderby.Spec, error) {
	return orderby.Spec{Keys: []orderby.Key{{Name: "id"}}}, nil
}

func TestInjectedParsers(t *testing.T) {
	t.Parallel()

	cause := errors.New("custom parser failure")
	s, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"custom"}, listing.NewProfile(listing.WithFilterParser(failingFilterParser{cause}), listing.WithOrderParser(singleOrderParser{}), listing.WithContractID("custom-v2")))
	require.NoError(t, err)
	require.Equal(t, "custom-v2", s.ContractID())
	_, err = s.Compile(t.Context(), listing.Request{Filter: "custom"})
	require.ErrorIs(t, err, cause)
}
