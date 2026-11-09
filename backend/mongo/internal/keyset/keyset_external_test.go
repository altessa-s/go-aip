// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package keyset_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/backend/mongo/internal/keyset"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

type tokenCodecFake struct{ err error }

func (f tokenCodecFake) Issue([]byte, pagetoken.Bindings) (string, error) {
	return "", f.err
}

func (f tokenCodecFake) Resolve(string, pagetoken.Bindings) ([]byte, error) {
	return nil, f.err
}

func TestFindPage_UsesTheSigner(t *testing.T) {
	t.Parallel()

	boom := errors.New("codec failure")
	_, err := keyset.FindPage[bson.M](t.Context(), nil, tokenCodecFake{err: boom}, keyset.Query{After: "token"})
	require.ErrorIs(t, err, boom)

	_, err = keyset.FindPage[bson.M](t.Context(), nil, (*pagetoken.Codec)(nil), keyset.Query{After: "token"})
	require.ErrorIs(t, err, keyset.ErrTokensRequired)
}

// TestBindingsOfATranslatedFilterAreStable checks the MongoDB translator's ordered output binds a page token stably: the same
// filter translated again resolves the token issued for it.
func TestBindingsOfATranslatedFilterAreStable(t *testing.T) {
	t.Parallel()

	schema, err := mongo.NewSchema(mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"note": filter.FieldKindString}))
	require.NoError(t, err)

	tr, err := mongo.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), schema)
	require.NoError(t, err)

	node := filtertest.MustParseFilter(t, `(rank > 3 || tags.size() <= 2) && note.startsWith("a") && !(kind in ["x", "y"])`)
	first, err := tr.TranslateD(node)
	require.NoError(t, err)

	want, err := bson.Marshal(first)
	require.NoError(t, err)

	for range 20 {
		again, err := tr.TranslateD(node)
		require.NoError(t, err)

		got, err := bson.Marshal(again)
		require.NoError(t, err)
		require.Equal(t, want, got, "the same filter binds the same token")
	}
}
