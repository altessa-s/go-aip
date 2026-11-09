// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestValueEncodingEqual pins the comparison of the BSON scalar types the encoding keeps: by ==, a binary by subtype and content,
// and a CodeWithScope, which holds a document, equal to nothing.
func TestValueEncodingEqual(t *testing.T) {
	t.Parallel()

	enc := valueEncoding{}
	require.True(t, enc.Equal(bson.ObjectID{1}, bson.ObjectID{1}))
	require.True(t, enc.Equal(bson.Regex{Pattern: "a", Options: "i"}, bson.Regex{Pattern: "a", Options: "i"}))
	require.True(t, enc.Equal(bson.Binary{Subtype: 4, Data: []byte{1}}, bson.Binary{Subtype: 4, Data: []byte{1}}))
	require.False(t, enc.Equal(bson.Binary{Subtype: 4, Data: []byte{1}}, bson.Binary{Subtype: 5, Data: []byte{1}}))
	require.False(t, enc.Equal(bson.CodeWithScope{Code: "f"}, bson.CodeWithScope{Code: "f"}))
	require.False(t, enc.Equal("a", "a"), "a base value is the evaluator's to compare")
}
