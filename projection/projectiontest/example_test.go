// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projectiontest_test

import (
	"fmt"
	"testing"

	"github.com/altessa-s/go-aip/projection"
	"github.com/altessa-s/go-aip/projection/projectiontest"
)

// A backend adapts its translator and declares what it can render beyond an
// inclusion list.
func ExampleRunConformance() {
	var t *testing.T // the *testing.T of a TestConformance function

	backend := projectiontest.Backend{
		New: func(p *projection.Policy, storage ...projection.TranslatorOption) (projectiontest.Translator, error) {
			ctx, err := projection.NewTranslatorContext(p, storage...)
			if err != nil {
				return nil, err
			}

			return projectiontest.Adapt(reference{ctx: ctx}, decode), nil
		},
		Capabilities: projectiontest.Capabilities{Exclusion: true, None: true},
	}

	if t != nil {
		projectiontest.RunConformance(t, backend)
	}

	fmt.Println(backend.Capabilities.Exclusion)
	// Output: true
}
