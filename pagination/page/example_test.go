// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package page_test

import (
	"fmt"

	"github.com/altessa-s/go-aip/pagination/page"
)

func ExamplePage() {
	p := page.Page[string]{Items: []string{"a", "b"}, NextPageToken: "next"}
	fmt.Println(p.Items, p.NextPageToken != "")
	// Output: [a b] true
}
