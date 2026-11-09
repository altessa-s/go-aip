// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory_test

import (
	"cmp"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

// TestListPagesMatchFullSort walks every page of a collection larger than the pages, for every order and several page sizes,
// and checks the pages concatenate to the collection sorted independently: the bounded heap and the cursor skip lose,
// repeat and reorder nothing.
func TestListPagesMatchFullSort(t *testing.T) {
	t.Parallel()

	created := func(w listingtest.Widget) time.Time { return w.Created.Truncate(time.Millisecond) }
	orders := map[string]func(a, b listingtest.Widget) int{
		"createTime desc": func(a, b listingtest.Widget) int {
			return cmp.Or(created(b).Compare(created(a)), cmp.Compare(b.ID, a.ID))
		},
		"name": func(a, b listingtest.Widget) int { return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID)) },
		"kind, priority desc": func(a, b listingtest.Widget) int {
			return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(b.Priority, a.Priority), cmp.Compare(b.ID, a.ID))
		},
		"priority, createTime": func(a, b listingtest.Widget) int {
			return cmp.Or(cmp.Compare(a.Priority, b.Priority), created(a).Compare(created(b)), cmp.Compare(a.ID, b.ID))
		},
		"id": func(a, b listingtest.Widget) int { return cmp.Compare(a.ID, b.ID) },
	}
	filters := map[string]func(listingtest.Widget) bool{
		"":             func(listingtest.Widget) bool { return true },
		"priority > 1": func(w listingtest.Widget) bool { return w.Priority > 1 },
	}

	ws := listingtest.MakeWidgets(400)
	l := newWidgets(t, listingtest.NewSchema(t), pagetokentest.Codec(t))

	for orderBy, compare := range orders {
		for filterText, keep := range filters {
			want := slices.DeleteFunc(slices.Clone(ws), func(w listingtest.Widget) bool { return !keep(w) })
			slices.SortFunc(want, compare)

			for _, size := range []int32{1, 7, 50, 500} {
				t.Run(fmt.Sprintf("%s/%s/%d", orderBy, filterText, size), func(t *testing.T) {
					t.Parallel()

					var got []listingtest.Widget

					req := listing.Request{PageSize: size, OrderBy: orderBy, Filter: filterText}
					for {
						p, err := l.List(t.Context(), listingtest.Subject, req, ws)
						require.NoError(t, err)
						require.LessOrEqual(t, len(p.Items), int(size))

						got = append(got, p.Items...)
						if p.NextPageToken == "" {
							break
						}

						require.Len(t, p.Items, int(size))
						req.PageToken = p.NextPageToken
					}

					require.Equal(t, widgetIDs(want), widgetIDs(got))
				})
			}
		}
	}
}

func widgetIDs(ws []listingtest.Widget) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.ID
	}

	return out
}
