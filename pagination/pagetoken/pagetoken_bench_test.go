// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

func BenchmarkCodec_Issue(b *testing.B) {
	c, _ := pagetoken.New(key)
	payload := []byte("1700000000000|01JABCDEFGHJKMNPQRSTVWXYZ00")

	b.ReportAllocs()

	for b.Loop() {
		_, _ = c.Issue(payload, bindings)
	}
}

func BenchmarkCodec_Resolve(b *testing.B) {
	c, _ := pagetoken.New(key)
	token, _ := c.Issue([]byte("1700000000000|01JABCDEFGHJKMNPQRSTVWXYZ00"), bindings)

	b.ReportAllocs()

	for b.Loop() {
		_, _ = c.Resolve(token, bindings)
	}
}

// BenchmarkCodec_IssueEncrypted measures issuing an encrypted (version 2)
// token, for comparison with BenchmarkCodec_Issue.
func BenchmarkCodec_IssueEncrypted(b *testing.B) {
	c, _ := pagetoken.New(key, pagetoken.WithEncryption(true))
	payload := []byte("1700000000000|01JABCDEFGHJKMNPQRSTVWXYZ00")

	b.ReportAllocs()

	for b.Loop() {
		_, _ = c.Issue(payload, bindings)
	}
}

// BenchmarkCodec_ResolveEncrypted measures resolving an encrypted (version
// 2) token, for comparison with BenchmarkCodec_Resolve.
func BenchmarkCodec_ResolveEncrypted(b *testing.B) {
	c, _ := pagetoken.New(key, pagetoken.WithEncryption(true))
	token, _ := c.Issue([]byte("1700000000000|01JABCDEFGHJKMNPQRSTVWXYZ00"), bindings)

	b.ReportAllocs()

	for b.Loop() {
		_, _ = c.Resolve(token, bindings)
	}
}

// BenchmarkCodec_ResolveParallel measures concurrent resolves sharing one
// Codec, including a token signed with a previous key.
func BenchmarkCodec_ResolveParallel(b *testing.B) {
	old, _ := pagetoken.New(otherKey)
	token, _ := old.Issue([]byte("1700000000000|01JABCDEFGHJKMNPQRSTVWXYZ00"), bindings)
	c, _ := pagetoken.New(key, pagetoken.WithPreviousKeys(otherKey))

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = c.Resolve(token, bindings)
		}
	})
}

func BenchmarkTyped_Resolve(b *testing.B) {
	c, _ := pagetoken.New(key)
	typed := pagetoken.NewTyped[position](c)
	token, _ := typed.Issue(position{Time: 1700000000000, ID: "01JABCDEFGHJKMNPQRSTVWXYZ00"}, bindings)

	b.ReportAllocs()

	for b.Loop() {
		_, _, _ = typed.Resolve(token, bindings)
	}
}

func BenchmarkNewBindings(b *testing.B) {
	b.ReportAllocs()

	for b.Loop() {
		_ = pagetoken.NewBindings("ListItems", "name:1", "tenant-42", "status=active", "")
	}
}

func BenchmarkClassify(b *testing.B) {
	err := fmt.Errorf("list: %w", pagetoken.ErrExpiredToken)

	b.ReportAllocs()

	for b.Loop() {
		_ = pagetoken.Classify(err)
	}
}

func BenchmarkSlicePager_Page(b *testing.B) {
	items := make([]item, 200)
	for i := range items {
		items[i] = item{name: fmt.Sprintf("item-%04d", i)}
	}

	codec, _ := pagetoken.New(key)
	pager := keysetPager(codec)
	first, _ := pager.Page(items, 50, "", sliceBindings)

	b.ReportAllocs()

	for b.Loop() {
		_, _ = pager.Page(items, 50, first.Next, sliceBindings)
	}
}

func BenchmarkSliceOffsetPager_Page(b *testing.B) {
	items := make([]item, 200)
	codec, _ := pagetoken.New(key)
	pager := pagetoken.NewSliceOffsetPager[item](pagetoken.NewOffset(codec))
	first, _ := pager.Page(items, 50, "", sliceBindings)

	b.ReportAllocs()

	for b.Loop() {
		_, _ = pager.Page(items, 50, first.Next, sliceBindings)
	}
}

func BenchmarkComparePositions(b *testing.B) {
	compare := pagetoken.ComparePositions([]bool{false, true}, true, pagetoken.ScalarPositionCodec{})
	name, _ := pagetoken.NewSortValue("item")
	low, _ := pagetoken.NewSortValue(int64(1))
	high, _ := pagetoken.NewSortValue(2.5)
	x := pagetoken.Position{Keys: []pagetoken.SortValue{name, low}, ID: benchSortValue("a")}
	y := pagetoken.Position{Keys: []pagetoken.SortValue{name, high}, ID: benchSortValue("b")}

	b.ReportAllocs()

	for b.Loop() {
		_ = compare(x, y)
	}
}

func BenchmarkNewSortValue(b *testing.B) {
	type named string

	b.ReportAllocs()

	for b.Loop() {
		_, _ = pagetoken.NewSortValue(named("item"))
	}
}

func benchSortValue(v any) pagetoken.SortValue { s, _ := pagetoken.NewSortValue(v); return s }

func BenchmarkPositionCodecNormalize(b *testing.B) {
	codec := pagetoken.ScalarPositionCodec{}
	at := time.Date(2026, 1, 1, 0, 0, 0, 123456789, time.UTC)

	b.ReportAllocs()

	for b.Loop() {
		_, _ = codec.Normalize(at, time.Microsecond)
	}
}

func benchPosition() pagetoken.Position {
	at := time.Date(2026, 1, 1, 0, 0, 0, 123000000, time.UTC)

	return pagetoken.Position{
		Keys: []pagetoken.SortValue{benchSortValue("item"), benchSortValue(int64(42)), benchSortValue(2.5), benchSortValue(true), benchSortValue(at)},
		ID:   benchSortValue("01JABCDEFGHJKMNPQRSTVWXYZ00"),
	}
}

func BenchmarkPositionMarshal(b *testing.B) {
	codec := pagetoken.ScalarPositionCodec{}
	pos := benchPosition()

	b.ReportAllocs()

	for b.Loop() {
		_, _ = codec.Marshal(pos)
	}
}

func BenchmarkPositionUnmarshal(b *testing.B) {
	codec := pagetoken.ScalarPositionCodec{}

	data, err := codec.Marshal(benchPosition())
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for b.Loop() {
		var pos pagetoken.Position

		_ = codec.Unmarshal(data, &pos)
	}
}

func BenchmarkPositionCodecNormalizeScalars(b *testing.B) {
	codec := pagetoken.ScalarPositionCodec{}
	values := []any{"item", int64(42), 2.5, true}

	b.ReportAllocs()

	for b.Loop() {
		for _, v := range values {
			_, _ = codec.Normalize(v, time.Millisecond)
		}
	}
}

func BenchmarkSortValueValue(b *testing.B) {
	values := []pagetoken.SortValue{benchSortValue("item"), benchSortValue(int64(42)), benchSortValue(2.5), benchSortValue(true)}

	b.ReportAllocs()

	for b.Loop() {
		for _, v := range values {
			_, _ = v.Value()
		}
	}
}
