// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory

import (
	"context"
	"fmt"
	"slices"

	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

// ErrWorkBudget reports an exhausted request scan, materialization, or work limit. It wraps [listing.ErrWorkBudget].
var ErrWorkBudget = fmt.Errorf("memory: %w", listing.ErrWorkBudget)

type workBudget struct {
	ctx       context.Context
	max, used int
	err       error
}

// Charge accounts evaluation work in the same budget as scans and sorting.
func (w *workBudget) Charge(units int) bool {
	if w.err != nil {
		return false
	}

	if err := w.ctx.Err(); err != nil {
		w.err = err
		return false
	}

	if units < 0 || (w.max > 0 && units > w.max-w.used) {
		w.err = ErrWorkBudget
		return false
	}

	if w.max > 0 {
		w.used += units
	}

	return true
}

func (w *workBudget) take() error {
	w.Charge(1)
	return w.err
}

// row is one matching item with its page position.
type row[T any] struct {
	item T
	pos  pagetoken.Position
}

// heapArity is the number of children of a node of the binary heap of [topRows].
const heapArity = 2

// topRows keeps the limit smallest rows offered to it in a max-heap, so a page retains O(limit) rows and costs O(n log limit)
// comparisons. Every comparison is charged to work, so cancellation and the work budget stop it without a panicking comparator.
type topRows[T any] struct {
	compare func(a, b pagetoken.Position) int
	work    *workBudget
	rows    []row[T]
	limit   int
	// maxMaterialized bounds the rows held; zero is unlimited.
	maxMaterialized int
}

// offer adds the row of item at *pos when it is among the limit smallest so far. The row copies pos: the caller may reuse it.
func (h *topRows[T]) offer(item T, pos *pagetoken.Position) error {
	if len(h.rows) < h.limit {
		if h.maxMaterialized > 0 && len(h.rows) >= h.maxMaterialized {
			return ErrWorkBudget
		}

		h.rows = append(h.rows, row[T]{item: item, pos: pagetoken.Position{Keys: slices.Clone(pos.Keys), ID: pos.ID}})

		return h.up(len(h.rows) - 1)
	}

	if err := h.work.take(); err != nil {
		return err
	}

	// Positions are unique, so an equal one is a duplicate id: the earlier item keeps its place, as a stable sort keeps it.
	if h.compare(*pos, h.rows[0].pos) >= 0 {
		return nil
	}

	// The evicted row's keys are reused: the heap allocates no keys once it is full.
	top := &h.rows[0]
	top.item = item
	copy(top.pos.Keys, pos.Keys)
	top.pos.ID = pos.ID

	return h.down(0, len(h.rows))
}

// sorted returns the rows held in ascending order.
func (h *topRows[T]) sorted() ([]row[T], error) {
	if err := h.work.ctx.Err(); err != nil {
		return nil, err
	}

	for end := len(h.rows) - 1; end > 0; end-- {
		h.rows[0], h.rows[end] = h.rows[end], h.rows[0]
		if err := h.down(0, end); err != nil {
			return nil, err
		}
	}

	return h.rows, h.work.ctx.Err()
}

// up restores the heap order from i towards the root.
func (h *topRows[T]) up(i int) error {
	for i > 0 {
		parent := (i - 1) / heapArity

		if err := h.work.take(); err != nil {
			return err
		}

		if h.compare(h.rows[i].pos, h.rows[parent].pos) <= 0 {
			return nil
		}

		h.rows[i], h.rows[parent] = h.rows[parent], h.rows[i]
		i = parent
	}

	return nil
}

// down restores the heap order of rows[:n] from i towards the leaves.
func (h *topRows[T]) down(i, n int) error {
	for {
		largest := heapArity*i + 1
		if largest >= n {
			return nil
		}

		if right := largest + 1; right < n {
			if err := h.work.take(); err != nil {
				return err
			}

			if h.compare(h.rows[right].pos, h.rows[largest].pos) > 0 {
				largest = right
			}
		}

		if err := h.work.take(); err != nil {
			return err
		}

		if h.compare(h.rows[i].pos, h.rows[largest].pos) >= 0 {
			return nil
		}

		h.rows[i], h.rows[largest] = h.rows[largest], h.rows[i]
		i = largest
	}
}
