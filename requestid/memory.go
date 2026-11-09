// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package requestid

import (
	"bytes"
	"container/heap"
	"context"
	"sync"
	"time"
)

// DefaultMaxEntries is the default capacity of a [MemoryStore].
const DefaultMaxEntries = 100_000

// MemoryStore is the in-memory reference [Store], for tests and single-node
// services. It is safe for concurrent use. Expired entries are dropped
// lazily, earliest expiry first, by every call that writes; a live entry is
// never dropped: when the store holds [WithMaxEntries] live entries,
// Reserve of a new key fails with [ErrStoreFull].
type MemoryStore struct {
	mu         sync.Mutex
	entries    map[string]*memoryItem
	expiry     expiryHeap
	maxEntries int
}

// NewMemoryStore returns an empty [MemoryStore].
func NewMemoryStore(opts ...MemoryStoreOption) *MemoryStore {
	o := newMemoryStoreOptions(opts...)

	return &MemoryStore{entries: make(map[string]*memoryItem), maxEntries: o.maxEntries}
}

var _ Store = (*MemoryStore)(nil)

// Reserve implements [Store.Reserve].
func (s *MemoryStore) Reserve(ctx context.Context, key string, pending Entry, now time.Time) (Entry, bool, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweep(now)

	if it, ok := s.entries[key]; ok {
		return cloneEntry(it.entry), false, nil
	}

	if s.maxEntries > 0 && len(s.entries) >= s.maxEntries {
		return Entry{}, false, ErrStoreFull
	}

	it := &memoryItem{key: key, entry: cloneEntry(pending)}
	s.entries[key] = it
	heap.Push(&s.expiry, it)

	return cloneEntry(pending), true, nil
}

// Complete implements [Store.Complete].
func (s *MemoryStore) Complete(ctx context.Context, key, token string, done Entry, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	it, err := s.owned(key, token, now)
	if err != nil {
		return err
	}

	it.entry = cloneEntry(done)
	heap.Fix(&s.expiry, it.index)

	return nil
}

// Release implements [Store.Release].
func (s *MemoryStore) Release(ctx context.Context, key, token string, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	it, err := s.owned(key, token, now)
	if err != nil {
		return err
	}

	s.remove(it)

	return nil
}

// owned returns the live pending item under key reserved with token.
func (s *MemoryStore) owned(key, token string, now time.Time) (*memoryItem, error) {
	s.sweep(now)

	it, ok := s.entries[key]
	if !ok || it.entry.State != StatePending || it.entry.Token != token {
		return nil, ErrReservationLost
	}

	return it, nil
}

// sweep drops every entry expired at now.
func (s *MemoryStore) sweep(now time.Time) {
	for len(s.expiry) > 0 && !s.expiry[0].entry.ExpiresAt.After(now) {
		s.remove(s.expiry[0])
	}
}

func (s *MemoryStore) remove(it *memoryItem) {
	heap.Remove(&s.expiry, it.index)
	delete(s.entries, it.key)
}

// cloneEntry copies e so that neither side shares its byte slices.
func cloneEntry(e Entry) Entry {
	e.Fingerprint = bytes.Clone(e.Fingerprint)
	e.Response = bytes.Clone(e.Response)
	e.Failure = bytes.Clone(e.Failure)

	return e
}

type memoryItem struct {
	key   string
	entry Entry
	index int
}

// expiryHeap orders items by expiry, earliest first.
type expiryHeap []*memoryItem

func (h expiryHeap) Len() int { return len(h) }

func (h expiryHeap) Less(i, j int) bool { return h[i].entry.ExpiresAt.Before(h[j].entry.ExpiresAt) }

func (h expiryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *expiryHeap) Push(x any) {
	it, _ := x.(*memoryItem)
	it.index = len(*h)
	*h = append(*h, it)
}

func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]

	return it
}
