// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package keyset

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func BenchmarkValidateFilter(b *testing.B) {
	filter := bson.M{"$or": []bson.D{{{Key: "name", Value: "a"}}, {{Key: "name", Value: "b"}}}}
	for b.Loop() {
		if err := ValidateFilter(b.Context(), filter, nil); err != nil {
			b.Fatal(err)
		}
	}
}
