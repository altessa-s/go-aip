// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

func TestBooleanEnumStorage(t *testing.T) {
	t.Parallel()

	s, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "enabled", Kind: filter.FieldKindBool, Enum: map[string]any{"ENABLED": true, "DISABLED": false}}}, []string{"id"}, nil)
	require.NoError(t, err)
	l, err := memory.NewListEngine(nil, s, func(v string) string { return v }, func(v string) map[string]any { return map[string]any{"id": v, "enabled": true} })
	require.NoError(t, err)
	page, err := l.List(t.Context(), "widgets.List", listing.Request{Filter: `enabled == "ENABLED"`}, []string{"a"})
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, page.Items)
}
