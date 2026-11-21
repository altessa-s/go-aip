// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-mongox/bsoncodec"

	"github.com/altessa-s/go-aip/backend/mongo"
)

func TestCodecSentinels(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, bsoncodec.ErrInvalidDate, mongo.ErrInvalidDate)
	require.ErrorIs(t, bsoncodec.ErrNotADateType, mongo.ErrNotADateType)
	require.ErrorIs(t, bsoncodec.ErrDecimalOutOfRange, mongo.ErrDecimalOutOfRange)
	require.ErrorIs(t, bsoncodec.ErrDecimalNotFinite, mongo.ErrDecimalNotFinite)
}

func TestRegisterDateTypeZeroAsEmptyString(t *testing.T) {
	t.Parallel()

	type date struct {
		Year  int
		Month time.Month
		Day   int
	}

	reg := mongo.NewRegistry()
	require.NoError(t, mongo.RegisterDateType[date](reg, mongo.WithDateZeroAsEmptyString()))

	var buf bytes.Buffer

	enc := bson.NewEncoder(bson.NewDocumentWriter(&buf))
	enc.SetRegistry(reg)
	require.NoError(t, enc.Encode(bson.D{{Key: "date", Value: date{}}}))

	got, ok := bson.Raw(buf.Bytes()).Lookup("date").StringValueOK()
	require.True(t, ok)
	require.Empty(t, got)
}
