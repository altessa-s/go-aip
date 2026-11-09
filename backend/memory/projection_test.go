// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/metrics"
	"github.com/altessa-s/go-aip/projection"
	"github.com/altessa-s/go-aip/projection/projectiontest"
)

func TestProjectionTranslatorConformance(t *testing.T) {
	t.Parallel()

	projectiontest.RunConformance(t, projectiontest.Backend{
		New: func(p *projection.Policy, storage ...projection.TranslatorOption) (projectiontest.Translator, error) {
			tr, err := memory.NewProjectionTranslator(p, memory.WithProjectionOptions(storage...))
			if err != nil {
				return nil, err
			}

			return projectiontest.Adapt(tr, decode), nil
		},
		Capabilities: projectiontest.Capabilities{Exclusion: true, None: true},
	})
}

func decode(p *memory.Projection) (projectiontest.Result, error) {
	sel := p.Selection()

	switch {
	case sel.Mode == projection.ModeNone:
		return projectiontest.Result{None: true}, nil
	case sel.IsAll():
		return projectiontest.Result{All: true}, nil
	default:
		return projectiontest.Result{Include: sel.Include, Exclude: sel.Exclude}, nil
	}
}

func document() map[string]any {
	return map[string]any{
		"_id":  "u1",
		"name": "Ada",
		"pass": "secret",
		"address": map[string]any{
			"city": "London", "zip": "N1", "secret": "x",
		},
		"items": []any{
			map[string]any{"sku": "a", "price": 1, "secret": "y"},
			"scalar",
			[]any{map[string]any{"sku": "b", "price": 2}},
		},
		"lines": []map[string]any{{"sku": "c", "qty": 3}},
		"title": "plain",
	}
}

func TestProjectionApplyInclude(t *testing.T) {
	t.Parallel()

	tr, err := memory.NewProjectionTranslator(projectiontest.MustPolicy(t),
		memory.WithProjectionOptions(projection.WithRequiredFields("_id")),
		memory.WithProjectionCollector(metrics.Noop()),
	)
	require.NoError(t, err)

	doc := document()

	tests := []struct {
		name  string
		paths []string
		want  map[string]any
	}{
		{
			name:  "top level",
			paths: []string{"name"},
			want:  map[string]any{"_id": "u1", "name": "Ada"},
		},
		{
			name:  "nested map",
			paths: []string{"address.city", "address.missing"},
			want:  map[string]any{"_id": "u1", "address": map[string]any{"city": "London"}},
		},
		{
			name:  "ancestor covers descendant",
			paths: []string{"address", "address.city"},
			want:  map[string]any{"_id": "u1", "address": doc["address"]},
		},
		{
			name:  "list elements",
			paths: []string{"items.sku", "lines.sku"},
			want: map[string]any{
				"_id": "u1",
				"items": []any{
					map[string]any{"sku": "a"},
					[]any{map[string]any{"sku": "b"}},
				},
				"lines": []map[string]any{{"sku": "c"}},
			},
		},
		{
			name:  "path through a scalar",
			paths: []string{"title.x"},
			want:  map[string]any{"_id": "u1"},
		},
		{
			name:  "missing map key keeps an empty map",
			paths: []string{"address.none"},
			want:  map[string]any{"_id": "u1", "address": map[string]any{}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := tr.Translate(projection.Spec{Paths: tc.paths})
			require.NoError(t, err)
			require.Equal(t, tc.want, p.Apply(doc))
			require.Equal(t, document(), doc, "the input is never modified")
		})
	}
}

func TestProjectionApplyExclude(t *testing.T) {
	t.Parallel()

	tr, err := memory.NewProjectionTranslator(projectiontest.MustPolicy(t,
		projection.WithDeniedFields("pass", "address.secret", "items.secret", "lines.qty", "title.x", "absent"),
	))
	require.NoError(t, err)

	doc := document()

	p, err := tr.Translate(projection.Spec{})
	require.NoError(t, err)
	require.Equal(t, []string{"absent", "address.secret", "items.secret", "lines.qty", "pass", "title.x"}, p.Selection().Exclude)

	require.Equal(t, map[string]any{
		"_id":     "u1",
		"name":    "Ada",
		"address": map[string]any{"city": "London", "zip": "N1"},
		"items": []any{
			map[string]any{"sku": "a", "price": 1},
			"scalar",
			[]any{map[string]any{"sku": "b", "price": 2}},
		},
		"lines": []map[string]any{{"sku": "c"}},
		"title": "plain",
	}, p.Apply(doc))
	require.Equal(t, document(), doc, "the input is never modified")
}

func TestProjectionApplyAllAndNone(t *testing.T) {
	t.Parallel()

	tr, err := memory.NewProjectionTranslator(projectiontest.MustPolicy(t))
	require.NoError(t, err)

	all, err := tr.TranslateView(projection.Spec{}, projection.ViewFull)
	require.NoError(t, err)
	require.Equal(t, document(), all.Apply(document()))
	require.Nil(t, all.Apply(nil))

	none, err := tr.Translate(projection.Spec{Mode: projection.ModeNone})
	require.NoError(t, err)
	require.Equal(t, map[string]any{}, none.Apply(document()))

	_, err = tr.TranslateView(projection.Spec{}, projection.View(42))
	require.ErrorIs(t, err, projection.ErrInvalidView)

	_, err = tr.Translate(projection.Spec{Paths: []string{"a.0"}})
	require.ErrorIs(t, err, projection.ErrUnsupportedPath)
}

// message returns a protobuf message with scalar, nested and repeated fields: a well-known descriptor message, so the module
// needs no generated test fixture.
func message() *descriptorpb.FileDescriptorProto {
	return &descriptorpb.FileDescriptorProto{
		Name:       new("u1"),
		Package:    new("ada"),
		Syntax:     new("proto3"),
		Options:    &descriptorpb.FileOptions{JavaPackage: new("london"), GoPackage: new("n1")},
		Dependency: []string{"a"},
	}
}

func TestProjectionApplyProto(t *testing.T) {
	t.Parallel()

	tr, err := memory.NewProjectionTranslator(projectiontest.MustPolicy(t, projection.WithDeniedFields("syntax", "options.go_package")),
		memory.WithProjectionOptions(projection.WithRequiredFields("name")))
	require.NoError(t, err)

	include, err := tr.Translate(projection.Spec{Paths: []string{"package", "options.java_package"}})
	require.NoError(t, err)

	msg := message()
	require.NoError(t, include.ApplyProto(msg))
	require.True(t, proto.Equal(&descriptorpb.FileDescriptorProto{
		Name: new("u1"), Package: new("ada"), Options: &descriptorpb.FileOptions{JavaPackage: new("london")},
	}, msg), "got %v", msg)

	exclude, err := tr.Translate(projection.Spec{})
	require.NoError(t, err)

	msg = message()
	require.NoError(t, exclude.ApplyProto(msg))
	require.True(t, proto.Equal(&descriptorpb.FileDescriptorProto{
		Name: new("u1"), Package: new("ada"), Options: &descriptorpb.FileOptions{JavaPackage: new("london")}, Dependency: []string{"a"},
	}, msg), "got %v", msg)

	all, err := memory.NewProjectionTranslator(projectiontest.MustPolicy(t))
	require.NoError(t, err)

	every, err := all.Translate(projection.Spec{})
	require.NoError(t, err)

	msg = message()
	require.NoError(t, every.ApplyProto(msg))
	require.True(t, proto.Equal(message(), msg))

	none, err := all.Translate(projection.Spec{Mode: projection.ModeNone})
	require.NoError(t, err)

	msg = message()
	require.NoError(t, none.ApplyProto(msg))
	require.True(t, proto.Equal(&descriptorpb.FileDescriptorProto{}, msg))
	require.NoError(t, none.ApplyProto(nil))

	bad, err := all.Translate(projection.Spec{Paths: []string{"unknown_field"}})
	require.NoError(t, err)

	msg = message()
	require.ErrorIs(t, bad.ApplyProto(msg), projection.ErrInvalidFieldPath)
	require.True(t, proto.Equal(message(), msg), "an invalid path leaves the message untouched")
}

func TestProjectionApplyConcurrent(t *testing.T) {
	t.Parallel()

	tr, err := memory.NewProjectionTranslator(projectiontest.MustPolicy(t))
	require.NoError(t, err)

	p, err := tr.Translate(projection.Spec{Paths: []string{"name"}})
	require.NoError(t, err)

	for range 8 {
		t.Run("worker", func(t *testing.T) {
			t.Parallel()

			msg := message()
			require.NoError(t, p.ApplyProto(msg))
			require.Equal(t, map[string]any{"name": "Ada"}, p.Apply(document()))
		})
	}
}
