// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package unreachable_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/altessa-s/go-aip/unreachable"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func ExampleRun() {
	region := func(id string, books []string, err error) unreachable.Source[string] {
		return unreachable.Source[string]{
			Name:  "projects/example/locations/" + id,
			Fetch: func(context.Context) ([]string, error) { return books, err },
		}
	}

	sources := []unreachable.Source[string]{
		region("us-east1", []string{"dune"}, nil),
		region("us-west1", nil, coreerrs.ErrUnavailable),
		region("europe-west2", []string{"emma"}, nil),
	}

	res, err := unreachable.Run(context.Background(), sources)
	fmt.Println(res.Items, res.Unreachable, err)

	_, err = unreachable.Run(context.Background(), sources, unreachable.WithPartialSuccess(false))
	fmt.Println(errors.Is(err, coreerrs.ErrUnavailable))
	// Output:
	// [dune emma] [projects/example/locations/us-west1] <nil>
	// true
}

func ExampleSet() {
	set := unreachable.NewSet(unreachable.WithMaxNames(10))
	_ = set.Add("projects/example/locations/us-west1", "projects/example/locations/europe-west2")

	names, err := set.Response(true)
	fmt.Println(names, err)
	// Output:
	// [projects/example/locations/europe-west2 projects/example/locations/us-west1] <nil>
}
