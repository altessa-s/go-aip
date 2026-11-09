// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

type enumStorage struct{ Code int }
type enumStorageCodec struct{}

func (enumStorageCodec) ValidateLiteral(value any) error {
	if _, ok := value.(enumStorage); !ok {
		return filter.ErrUnsupportedType
	}

	return nil
}

func TestCustomEnumStorageCodec(t *testing.T) {
	t.Parallel()

	const kind filter.FieldKind = 200

	s, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "state", Kind: kind, Enum: map[string]any{"ACTIVE": enumStorage{1}}}}, []string{"id"}, listing.NewProfile(listing.WithFilterOptions(filter.WithValueCodecs(map[filter.FieldKind]filter.ValueCodec{kind: enumStorageCodec{}}))))
	require.NoError(t, err)
	_, err = s.Compile(t.Context(), listing.Request{Filter: `state == "ACTIVE"`})
	require.NoError(t, err)
}
