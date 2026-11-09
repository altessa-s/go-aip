// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package unreachable_test

import (
	"context"
	"fmt"
	"slices"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/unreachable"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// cursor is the page token payload of a merged list: the sort key of the
// last item served. One global position serves every source, because
// every source is read in the same order.
type cursor struct {
	After string `json:"after"`
}

// region is one source of the merged list: its titles in sort order.
type region struct {
	name   string
	titles []string
	down   bool
}

// after reads at most limit titles after the cursor, as a store would with
// WHERE title > ? ORDER BY title LIMIT ?.
func (r region) after(ctx context.Context, c cursor, limit int) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if r.down {
		return nil, coreerrs.ErrUnavailable
	}

	var out []string

	for _, t := range r.titles {
		if t > c.After && len(out) < limit {
			out = append(out, t)
		}
	}

	return out, nil
}

// listPage serves one page of the titles of every region in title order:
// each source reads size+1 titles after the token's position, the merged
// items are sorted and cut to size, and the next token carries the last
// title served. More than size merged titles means another page exists.
func listPage(ctx context.Context, tokens pagetoken.Typed[cursor], regions []region, size int, token string) (
	titles, unreachableNames []string, next string, err error,
) {
	binding := pagetoken.NewBindings("/library.v1.Library/ListBooks", "title", "")

	pos, _, err := tokens.Resolve(token, binding)
	if err != nil {
		return nil, nil, "", pagetoken.TokenFieldError(err)
	}

	sources := make([]unreachable.Source[string], len(regions))
	for i, r := range regions {
		sources[i] = unreachable.Source[string]{
			Name:  "projects/example/locations/" + r.name,
			Fetch: func(ctx context.Context) ([]string, error) { return r.after(ctx, pos, size+1) },
		}
	}

	res, err := unreachable.Run(ctx, sources)
	if err != nil {
		return nil, nil, "", err
	}

	titles = res.Items
	slices.Sort(titles) // Run merges in source order; the page has its own

	if len(titles) > size {
		titles = titles[:size]
		if next, err = tokens.Issue(cursor{After: titles[size-1]}, binding); err != nil {
			return nil, nil, "", err
		}
	}

	return titles, res.Unreachable, next, nil
}

// Example_pagination pages a List merged from three regions with one
// global keyset position in a signed page token. us-west1 is down on the
// first page and back on the second: its titles after the position appear
// there, those before it ("bee", "cat") are behind the client's position
// and are not served, as AIP-217 allows ("if allowed by paging
// parameters").
func Example_pagination() {
	codec, err := pagetoken.New([]byte("an example key of at least 32 bytes!!"))
	if err != nil {
		panic(err)
	}

	tokens := pagetoken.NewTyped[cursor](codec)
	regions := []region{
		{name: "us-east1", titles: []string{"ant", "dog", "owl"}},
		{name: "us-west1", titles: []string{"bee", "cat", "yak"}, down: true},
		{name: "europe-west2", titles: []string{"elk", "fox", "gnu"}},
	}

	ctx := context.Background()

	page, names, next, _ := listPage(ctx, tokens, regions, 3, "")
	fmt.Println(page, names)

	regions[1].down = false

	page, names, next, _ = listPage(ctx, tokens, regions, 3, next)
	fmt.Println(page, names)

	page, names, next, _ = listPage(ctx, tokens, regions, 3, next)
	fmt.Println(page, names, next == "")
	// Output:
	// [ant dog elk] [projects/example/locations/us-west1]
	// [fox gnu owl] []
	// [yak] [] true
}
