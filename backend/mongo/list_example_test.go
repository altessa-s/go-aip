// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"fmt"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

// widget is a stored document of the example list.
type widget struct {
	ID      string `bson:"_id"`
	Name    string `bson:"name"`
	Created int64  `bson:"created_at"`
}

// ExampleNewListEngine declares a list whose schema and paths are fixed in code. A nil collection plans only (sorts and index
// models); a store passes its collection and page-token signer.
func ExampleNewListEngine() {
	schema := listing.MustSchema(
		[]listing.Field{
			{Name: "id", Kind: filter.FieldKindString},
			{Name: "name", Kind: filter.FieldKindString},
			{Name: "createTime", Kind: filter.FieldKindTimestamp},
		},
		[]string{"createTime desc", "name"},
		nil)

	widgets, err := mongo.NewListEngine[widget](nil, nil, schema,
		mongo.WithListFieldPaths(map[string]string{"id": "_id", "createTime": "created_at"}),
	)
	if err != nil {
		panic(err)
	}

	fmt.Println(widgets.Sort(schema.Default()))
	fmt.Println(widgets.Index(schema.Default()))
	// Output:
	// {"created_at":{"$numberInt":"-1"},"_id":{"$numberInt":"-1"}}
	// created_at_-1__id_-1
}
