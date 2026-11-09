// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// unlimited is a data budget that never runs out.
type unlimited struct{}

func (unlimited) Charge(int) bool { return true }

func BenchmarkValueEncodingStored(b *testing.B) {
	type label struct {
		Kind string `bson:"kind"`
		N    int    `bson:"n"`
	}
	for name, v := range map[string]any{
		"bson.D": bson.D{{Key: "kind", Value: "tool"}, {Key: "n", Value: 1}},
		"struct": label{Kind: "tool", N: 1},
	} {
		b.Run(name, func(b *testing.B) {
			enc := valueEncoding{}
			for b.Loop() {
				if _, handled := enc.Stored(v, unlimited{}); !handled {
					_ = enc.Encode(v, unlimited{})
				}
			}
		})
	}
}
