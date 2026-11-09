// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"fmt"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/projection"
	"github.com/altessa-s/go-aip/projection/projectiontest"
)

func TestProjectionConformance(t *testing.T) {
	t.Parallel()

	projectiontest.RunConformance(t, projectiontest.Backend{
		New: func(p *projection.Policy, storage ...projection.TranslatorOption) (projectiontest.Translator, error) {
			tr, err := mongo.NewProjectionTranslator(p, mongo.WithProjectionOptions(storage...))
			if err != nil {
				return nil, err
			}

			return projectiontest.Adapt(tr, decode), nil
		},
		Capabilities: projectiontest.Capabilities{Exclusion: true},
	})
}

// decode reads a projection document back into storage paths. The implicit
// {_id: 0} of an inclusion projection is not a selected path.
func decode(doc bson.M) (projectiontest.Result, error) {
	if doc == nil {
		return projectiontest.Result{All: true}, nil
	}

	var r projectiontest.Result

	for path, v := range doc {
		switch v {
		case int32(1):
			r.Include = append(r.Include, path)
		case int32(0):
			r.Exclude = append(r.Exclude, path)
		default:
			return r, fmt.Errorf("unexpected projection value %v for %q", v, path)
		}
	}

	if len(r.Include) > 0 {
		if len(r.Exclude) > 1 || (len(r.Exclude) == 1 && r.Exclude[0] != "_id") {
			return r, fmt.Errorf("mixed projection %v", doc)
		}

		r.Exclude = nil
	}

	return r, nil
}
