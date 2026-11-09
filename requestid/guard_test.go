// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package requestid_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"

	"github.com/altessa-s/go-atlas-core/time/clock"

	"github.com/altessa-s/go-aip/requestid"
	"github.com/altessa-s/go-aip/requestid/requestidtest"

	v1 "github.com/altessa-s/go-aip/internal/proto/gen/requestidtest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
)

const (
	testID     = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	testMethod = "/library.v1.LibraryService/CreateBook"
)

var testScope = requestid.Scope{Method: testMethod, Principal: "users/alice"}

// counter is a handler that creates a book and counts its runs.
type counter struct {
	runs atomic.Int64
	err  error
}

func (c *counter) handle(_ context.Context, req *v1.CreateBookRequest) (*v1.Book, error) {
	n := c.runs.Add(1)
	if c.err != nil {
		return nil, c.err
	}

	return &v1.Book{Name: req.GetParent() + "/books/" + req.GetBookId(), Title: req.GetBook().GetTitle(), Cover: []byte{byte(n)}}, nil
}

func newRequest(id string) *v1.CreateBookRequest {
	return &v1.CreateBookRequest{Parent: "publishers/p1", BookId: "b1", Book: &v1.Book{Title: "Dune"}, RequestId: id}
}

func newGuard(t *testing.T, opts ...requestid.Option) *requestid.Guard {
	t.Helper()

	return newGuardOn(t, requestid.NewMemoryStore(), opts...)
}

func newGuardOn(t *testing.T, s requestid.Store, opts ...requestid.Option) *requestid.Guard {
	t.Helper()

	g, err := requestid.New(s, opts...)
	require.NoError(t, err)

	return g
}

func do(t *testing.T, g *requestid.Guard, scope requestid.Scope, req *v1.CreateBookRequest, h *counter) (*v1.Book, error) {
	t.Helper()

	return requestid.Do(t.Context(), g, scope, req, h.handle)
}

// hookStore wraps a store and lets a test intercept its calls.
type hookStore struct {
	requestid.Store

	reserve  func(ctx context.Context, key string, e requestid.Entry, now time.Time) (requestid.Entry, bool, error)
	complete func(ctx context.Context, key, token string, e requestid.Entry, now time.Time) error
	release  func(ctx context.Context, key, token string, now time.Time) error
}

func (s *hookStore) Reserve(ctx context.Context, key string, e requestid.Entry, now time.Time) (requestid.Entry, bool, error) {
	if s.reserve != nil {
		return s.reserve(ctx, key, e, now)
	}

	return s.Store.Reserve(ctx, key, e, now)
}

func (s *hookStore) Complete(ctx context.Context, key, token string, e requestid.Entry, now time.Time) error {
	if s.complete != nil {
		return s.complete(ctx, key, token, e, now)
	}

	return s.Store.Complete(ctx, key, token, e, now)
}

func (s *hookStore) Release(ctx context.Context, key, token string, now time.Time) error {
	if s.release != nil {
		return s.release(ctx, key, token, now)
	}

	return s.Store.Release(ctx, key, token, now)
}

func TestMemoryStoreConformance(t *testing.T) {
	t.Parallel()

	requestidtest.Run(t, func(*testing.T) requestid.Store { return requestid.NewMemoryStore() })
}

func TestNew(t *testing.T) {
	t.Parallel()

	_, err := requestid.New(nil)
	require.ErrorContains(t, err, "store is nil")

	var typedNil *requestid.MemoryStore

	_, err = requestid.New(typedNil)
	require.ErrorContains(t, err, "store is nil")

	_, err = requestid.New(requestid.NewMemoryStore(), requestid.WithFormat(requestid.Format(7)))
	require.ErrorContains(t, err, "Format(7)")
}

func TestDoWithoutRequestID(t *testing.T) {
	t.Parallel()

	reserved := false
	s := &hookStore{Store: requestid.NewMemoryStore(), reserve: func(context.Context, string, requestid.Entry, time.Time) (requestid.Entry, bool, error) {
		reserved = true
		return requestid.Entry{}, false, nil
	}}
	g := newGuardOn(t, s)
	h := &counter{}

	for range 2 {
		_, err := do(t, g, requestid.Scope{}, newRequest(""), h)
		require.NoError(t, err)
	}

	assert.EqualValues(t, 2, h.runs.Load(), "no ID: no deduplication")
	assert.False(t, reserved, "no ID: the store is not consulted")
}

func TestDoInvalidRequestID(t *testing.T) {
	t.Parallel()

	g := newGuard(t, requestid.WithFormat(requestid.FormatUUID4))
	h := &counter{}

	_, err := do(t, g, testScope, newRequest("01890a5d-ac96-774b-bcce-b302099a8057"), h)
	require.ErrorIs(t, err, requestid.ErrInvalidRequestID)
	require.ErrorIs(t, err, coreerrs.ErrInvalidArgument)

	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok)
	assert.Equal(t, "requestId", fe.Field)
	assert.Equal(t, "the request ID must be a version 4 UUID", fe.Reason)
	assert.Zero(t, h.runs.Load())
}

func TestDoReplaysDuplicate(t *testing.T) {
	t.Parallel()

	g := newGuard(t)
	h := &counter{}

	first, err := do(t, g, testScope, newRequest(testID), h)
	require.NoError(t, err)

	// The same ID in another letter case is the same UUID (AIP-202).
	second, err := do(t, g, testScope, newRequest("F47AC10B-58CC-4372-A567-0E02B2C3D479"), h)
	require.NoError(t, err)

	assert.EqualValues(t, 1, h.runs.Load())
	assert.True(t, proto.Equal(first, second), "the duplicate gets the stored response")
	assert.NotSame(t, first, second)
}

func TestDoRequestIDReused(t *testing.T) {
	t.Parallel()

	g := newGuard(t)
	h := &counter{}

	_, err := do(t, g, testScope, newRequest(testID), h)
	require.NoError(t, err)

	other := newRequest(testID)
	other.Book.Title = "Emma"

	_, err = do(t, g, testScope, other, h)
	require.ErrorIs(t, err, requestid.ErrRequestIDReused)
	require.ErrorIs(t, err, coreerrs.ErrAlreadyExists)
	assert.EqualValues(t, 1, h.runs.Load())
}

func TestDoIgnoredFields(t *testing.T) {
	t.Parallel()

	g := newGuard(t, requestid.WithIgnoredFields("validate_only", "not_a_field"))
	h := &counter{}

	_, err := do(t, g, testScope, newRequest(testID), h)
	require.NoError(t, err)

	other := newRequest(testID)
	other.ValidateOnly = true

	_, err = do(t, g, testScope, other, h)
	require.NoError(t, err)
	assert.EqualValues(t, 1, h.runs.Load())

	other.PageSize = 3
	_, err = do(t, g, testScope, other, h)
	require.ErrorIs(t, err, requestid.ErrRequestIDReused)
}

func TestDoScopeIsolation(t *testing.T) {
	t.Parallel()

	g := newGuard(t)
	h := &counter{}

	scopes := []requestid.Scope{
		testScope,
		{Method: testMethod, Principal: "users/bob"},
		{Method: "/library.v1.LibraryService/UpdateBook", Principal: "users/alice"},
		{Method: testMethod},
		// Length prefixes keep part boundaries: no collision by concatenation.
		{Method: testMethod + "users/alice"},
	}
	for _, sc := range scopes {
		_, err := do(t, g, sc, newRequest(testID), h)
		require.NoError(t, err)
	}

	assert.EqualValues(t, len(scopes), h.runs.Load())
}

func TestDoInvalidScope(t *testing.T) {
	t.Parallel()

	_, err := do(t, newGuard(t), requestid.Scope{Principal: "users/alice"}, newRequest(testID), &counter{})
	require.ErrorIs(t, err, requestid.ErrInvalidScope)
}

func TestDoRequestIDField(t *testing.T) {
	t.Parallel()

	h := func(context.Context, *v1.GetBookRequest) (*v1.Book, error) { return &v1.Book{}, nil }
	_, err := requestid.Do(t.Context(), newGuard(t), testScope, &v1.GetBookRequest{Name: "x"}, h)
	require.ErrorIs(t, err, requestid.ErrNoRequestIDField)

	for _, field := range []string{"page_size", "tags", "requestId"} {
		_, err = do(t, newGuard(t, requestid.WithField(field)), testScope, newRequest(testID), &counter{})
		require.ErrorIs(t, err, requestid.ErrNoRequestIDField, field)
	}

	// Another string field may carry the ID; the request_id field is then
	// an ordinary parameter of the fingerprint.
	g := newGuard(t, requestid.WithField("book_id"), requestid.WithFormat(requestid.FormatASCII))
	c := &counter{}

	for range 2 {
		_, err = do(t, g, testScope, newRequest(""), c)
		require.NoError(t, err)
	}

	assert.EqualValues(t, 1, c.runs.Load())
}

func TestDoNilRequest(t *testing.T) {
	t.Parallel()

	_, err := do(t, newGuard(t), testScope, nil, &counter{})
	require.ErrorIs(t, err, coreerrs.ErrInvalidArgument)
}

func TestDoFailureNotCached(t *testing.T) {
	t.Parallel()

	g := newGuard(t)
	h := &counter{err: errors.New("boom")}

	for range 2 {
		_, err := do(t, g, testScope, newRequest(testID), h)
		require.ErrorIs(t, err, h.err)
	}

	assert.EqualValues(t, 2, h.runs.Load(), "a failure releases the ID for a retry")

	h.err = nil

	_, err := do(t, g, testScope, newRequest(testID), h)
	require.NoError(t, err)

	_, err = do(t, g, testScope, newRequest(testID), h)
	require.NoError(t, err)
	assert.EqualValues(t, 3, h.runs.Load())
}

func TestDoFailureCached(t *testing.T) {
	t.Parallel()

	errPermanent := errors.New("permanent")
	errTransient := errors.New("transient")
	cache := func(err error) *statuspb.Status {
		switch {
		case errors.Is(err, errPermanent):
			return &statuspb.Status{Code: 9, Message: "precondition failed"}
		case errors.Is(err, errTransient):
			return &statuspb.Status{Code: 0} // OK is not a failure: not cached
		default:
			return nil
		}
	}

	g := newGuard(t, requestid.WithCacheError(cache))

	h := &counter{err: errPermanent}
	_, err := do(t, g, testScope, newRequest(testID), h)
	require.ErrorIs(t, err, errPermanent)

	_, err = do(t, g, testScope, newRequest(testID), h)
	require.ErrorIs(t, err, requestid.ErrReplayedFailure)

	re, ok := errors.AsType[*requestid.ReplayedError](err)
	require.True(t, ok)
	assert.EqualValues(t, 9, re.Status.GetCode())
	assert.Equal(t, "precondition failed", re.Status.GetMessage())
	assert.Equal(t, "requestid: replayed failure: code 9: precondition failed", re.Error())
	assert.EqualValues(t, 1, h.runs.Load())

	other := &counter{err: errTransient}
	scope := requestid.Scope{Method: testMethod, Principal: "users/bob"}

	for range 2 {
		_, err = do(t, g, scope, newRequest(testID), other)
		require.ErrorIs(t, err, errTransient)
	}

	assert.EqualValues(t, 2, other.runs.Load())
}

func TestDoResponseNotRetained(t *testing.T) {
	t.Parallel()

	g := newGuard(t, requestid.WithMaxResponseSize(16))
	big := func(context.Context, *v1.CreateBookRequest) (*v1.Book, error) {
		return &v1.Book{Cover: bytes.Repeat([]byte{1}, 64)}, nil
	}

	resp, err := requestid.Do(t.Context(), g, testScope, newRequest(testID), big)
	require.NoError(t, err)
	assert.Len(t, resp.GetCover(), 64, "the caller gets the response")

	_, err = requestid.Do(t.Context(), g, testScope, newRequest(testID), big)
	require.ErrorIs(t, err, requestid.ErrResponseNotRetained)
	require.ErrorIs(t, err, coreerrs.ErrFailedPrecondition)

	// Zero is unlimited.
	g = newGuard(t, requestid.WithMaxResponseSize(0))
	_, err = requestid.Do(t.Context(), g, testScope, newRequest(testID), big)
	require.NoError(t, err)
	resp, err = requestid.Do(t.Context(), g, testScope, newRequest(testID), big)
	require.NoError(t, err)
	assert.Len(t, resp.GetCover(), 64)
}

func TestDoRetention(t *testing.T) {
	t.Parallel()

	fake := clock.NewFake(requestidtest.Base)
	g := newGuard(t, requestid.WithClock(fake), requestid.WithRetention(time.Hour))
	h := &counter{}

	_, err := do(t, g, testScope, newRequest(testID), h)
	require.NoError(t, err)

	fake.Advance(time.Hour - time.Second)

	_, err = do(t, g, testScope, newRequest(testID), h)
	require.NoError(t, err)
	assert.EqualValues(t, 1, h.runs.Load())

	fake.Advance(time.Second)

	_, err = do(t, g, testScope, newRequest(testID), h)
	require.NoError(t, err)
	assert.EqualValues(t, 2, h.runs.Load(), "the ID is honored for the retention only")
}

func TestDoInProgress(t *testing.T) {
	t.Parallel()

	fake := clock.NewFake(requestidtest.Base)
	g := newGuard(t, requestid.WithClock(fake), requestid.WithWaitTimeout(time.Second), requestid.WithLeaseTTL(time.Hour))

	var inner error

	h := func(ctx context.Context, req *v1.CreateBookRequest) (*v1.Book, error) {
		// A duplicate while the first request runs waits, then gives up.
		start := fake.Now()
		_, inner = requestid.Do(ctx, g, testScope, req, func(context.Context, *v1.CreateBookRequest) (*v1.Book, error) {
			return nil, errors.New("the duplicate must not run")
		})

		assert.Equal(t, time.Second, fake.Now().Sub(start), "the duplicate waits the wait timeout")

		return &v1.Book{Name: "b"}, nil
	}

	_, err := requestid.Do(t.Context(), g, testScope, newRequest(testID), h)
	require.NoError(t, err)
	require.ErrorIs(t, inner, requestid.ErrInProgress)
	require.ErrorIs(t, inner, coreerrs.ErrBusy)
}

func TestDoInProgressNoWait(t *testing.T) {
	t.Parallel()

	g := newGuard(t, requestid.WithWaitTimeout(0))

	var inner error

	h := func(ctx context.Context, req *v1.CreateBookRequest) (*v1.Book, error) {
		_, inner = requestid.Do(ctx, g, testScope, req, func(context.Context, *v1.CreateBookRequest) (*v1.Book, error) {
			return nil, errors.New("the duplicate must not run")
		})

		return &v1.Book{}, nil
	}

	_, err := requestid.Do(t.Context(), g, testScope, newRequest(testID), h)
	require.NoError(t, err)
	require.ErrorIs(t, inner, requestid.ErrInProgress)
}

// sleepErrClock is a clock whose Sleep fails.
type sleepErrClock struct{ clock.Clock }

func (sleepErrClock) Sleep(context.Context, time.Duration) error { return context.Canceled }

func TestDoWaitCanceled(t *testing.T) {
	t.Parallel()

	g := newGuard(t, requestid.WithClock(sleepErrClock{clock.Real()}))

	var inner error

	h := func(ctx context.Context, req *v1.CreateBookRequest) (*v1.Book, error) {
		_, inner = requestid.Do(ctx, g, testScope, req, (&counter{}).handle)
		return &v1.Book{}, nil
	}

	_, err := requestid.Do(t.Context(), g, testScope, newRequest(testID), h)
	require.NoError(t, err)
	require.ErrorIs(t, inner, context.Canceled)
}

func TestDoConcurrentDuplicates(t *testing.T) {
	t.Parallel()

	g := newGuard(t, requestid.WithPollInterval(time.Millisecond))
	release := make(chan struct{})

	var runs atomic.Int64

	h := func(context.Context, *v1.CreateBookRequest) (*v1.Book, error) {
		runs.Add(1)
		<-release

		return &v1.Book{Name: "publishers/p1/books/b1"}, nil
	}

	const n = 16

	results := make([]*v1.Book, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { results[i], errs[i] = requestid.Do(t.Context(), g, testScope, newRequest(testID), h) })
	}

	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	assert.EqualValues(t, 1, runs.Load(), "concurrent duplicates run the handler once")

	for i := range n {
		require.NoError(t, errs[i])
		assert.Equal(t, "publishers/p1/books/b1", results[i].GetName())
	}
}

func TestDoLeaseExpiredTakeover(t *testing.T) {
	t.Parallel()

	fake := clock.NewFake(requestidtest.Base)

	var storeErrs []error

	g := newGuard(t, requestid.WithClock(fake), requestid.WithLeaseTTL(time.Minute),
		requestid.WithStoreErrorHandler(func(_ context.Context, err error) { storeErrs = append(storeErrs, err) }))

	inner := &counter{}
	h := func(ctx context.Context, req *v1.CreateBookRequest) (*v1.Book, error) {
		// The first run outlives its lease: a retry takes the ID over.
		fake.Advance(2 * time.Minute)

		_, err := requestid.Do(ctx, g, testScope, req, inner.handle)
		require.NoError(t, err)

		return &v1.Book{Name: "late"}, nil
	}

	resp, err := requestid.Do(t.Context(), g, testScope, newRequest(testID), h)
	require.NoError(t, err)
	assert.Equal(t, "late", resp.GetName(), "the first caller still gets its own response")
	assert.EqualValues(t, 1, inner.runs.Load())
	require.Len(t, storeErrs, 1)
	require.ErrorIs(t, storeErrs[0], requestid.ErrReservationLost)

	// The takeover's response is the one stored.
	replayed, err := do(t, g, testScope, newRequest(testID), &counter{})
	require.NoError(t, err)
	assert.Equal(t, "publishers/p1/books/b1", replayed.GetName())
}

func TestDoHandlerPanic(t *testing.T) {
	t.Parallel()

	g := newGuard(t)

	require.Panics(t, func() {
		_, _ = requestid.Do(t.Context(), g, testScope, newRequest(testID),
			func(context.Context, *v1.CreateBookRequest) (*v1.Book, error) { panic("boom") })
	})

	h := &counter{}
	_, err := do(t, g, testScope, newRequest(testID), h)
	require.NoError(t, err)
	assert.EqualValues(t, 1, h.runs.Load(), "a panic releases the ID")
}

func TestDoStoreErrors(t *testing.T) {
	t.Parallel()

	errDown := errors.New("down")

	t.Run("Reserve", func(t *testing.T) {
		t.Parallel()

		s := &hookStore{Store: requestid.NewMemoryStore(), reserve: func(context.Context, string, requestid.Entry, time.Time) (requestid.Entry, bool, error) {
			return requestid.Entry{}, false, errDown
		}}
		h := &counter{}

		_, err := do(t, newGuardOn(t, s), testScope, newRequest(testID), h)
		require.ErrorIs(t, err, requestid.ErrStore)
		require.ErrorIs(t, err, coreerrs.ErrUnavailable)
		require.ErrorIs(t, err, errDown)
		assert.Zero(t, h.runs.Load(), "fail closed: the handler does not run")
	})

	t.Run("Complete", func(t *testing.T) {
		t.Parallel()

		var got []error

		s := &hookStore{Store: requestid.NewMemoryStore(), complete: func(context.Context, string, string, requestid.Entry, time.Time) error {
			return errDown
		}}
		g := newGuardOn(t, s, requestid.WithStoreErrorHandler(func(_ context.Context, err error) { got = append(got, err) }))

		resp, err := do(t, g, testScope, newRequest(testID), &counter{})
		require.NoError(t, err)
		assert.NotNil(t, resp)
		require.Len(t, got, 1)
		require.ErrorIs(t, got[0], errDown)
	})

	t.Run("ReleaseWithoutHandler", func(t *testing.T) {
		t.Parallel()

		s := &hookStore{Store: requestid.NewMemoryStore(), release: func(context.Context, string, string, time.Time) error {
			return errDown
		}}
		h := &counter{err: errors.New("boom")}

		_, err := do(t, newGuardOn(t, s), testScope, newRequest(testID), h)
		require.ErrorIs(t, err, h.err)
	})

	t.Run("Full", func(t *testing.T) {
		t.Parallel()

		g := newGuardOn(t, requestid.NewMemoryStore(requestid.WithMaxEntries(1)))
		_, err := do(t, g, testScope, newRequest(testID), &counter{})
		require.NoError(t, err)

		_, err = do(t, g, requestid.Scope{Method: testMethod}, newRequest(testID), &counter{})
		require.ErrorIs(t, err, requestid.ErrStore)
		require.ErrorIs(t, err, requestid.ErrStoreFull)
	})
}

func TestDoCorruptEntry(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		corrupt func(*requestid.Entry)
		cache   func(error) *statuspb.Status
		err     error
	}{
		{name: "Response", corrupt: func(e *requestid.Entry) { e.Response = []byte{0xff} }},
		{
			name: "Failure", corrupt: func(e *requestid.Entry) { e.Failure = []byte{0xff} },
			cache: func(error) *statuspb.Status { return &statuspb.Status{Code: 5} }, err: errors.New("boom"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mem := requestid.NewMemoryStore()
			s := &hookStore{Store: mem, complete: func(ctx context.Context, key, token string, e requestid.Entry, now time.Time) error {
				tc.corrupt(&e)
				return mem.Complete(ctx, key, token, e, now)
			}}
			g := newGuardOn(t, s, requestid.WithCacheError(tc.cache))
			h := &counter{err: tc.err}

			_, _ = do(t, g, testScope, newRequest(testID), h)
			_, err := do(t, g, testScope, newRequest(testID), h)
			require.ErrorIs(t, err, requestid.ErrStore)
			assert.EqualValues(t, 1, h.runs.Load())
		})
	}
}

func TestDoInterfaceResponseType(t *testing.T) {
	t.Parallel()

	g := newGuard(t)
	h := func(context.Context, *v1.CreateBookRequest) (proto.Message, error) { return &v1.Book{}, nil }

	_, err := requestid.Do(t.Context(), g, testScope, newRequest(testID), h)
	require.NoError(t, err)

	_, err = requestid.Do(t.Context(), g, testScope, newRequest(testID), h)
	require.ErrorContains(t, err, "concrete message type")
}

func TestMemoryStoreCapacity(t *testing.T) {
	t.Parallel()

	s := requestid.NewMemoryStore(requestid.WithMaxEntries(2))
	ctx := t.Context()
	base := requestidtest.Base
	entry := func(token string, ttl time.Duration) requestid.Entry {
		return requestid.Entry{State: requestid.StatePending, Token: token, ExpiresAt: base.Add(ttl)}
	}

	_, ok, err := s.Reserve(ctx, "k1", entry("a", time.Minute), base)
	require.NoError(t, err)
	require.True(t, ok)
	_, ok, err = s.Reserve(ctx, "k2", entry("b", time.Hour), base)
	require.NoError(t, err)
	require.True(t, ok)

	_, _, err = s.Reserve(ctx, "k3", entry("c", time.Hour), base)
	require.ErrorIs(t, err, requestid.ErrStoreFull, "a live entry is never evicted")

	// A live key is still answered when the store is full.
	_, ok, err = s.Reserve(ctx, "k1", entry("x", time.Hour), base)
	require.NoError(t, err)
	require.False(t, ok)

	// Once k1 expires its slot is free.
	_, ok, err = s.Reserve(ctx, "k3", entry("c", time.Hour), base.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, ok)

	// Zero is unlimited.
	u := requestid.NewMemoryStore(requestid.WithMaxEntries(0))
	for i := range 3 {
		_, ok, err = u.Reserve(ctx, string(rune('a'+i)), entry("t", time.Hour), base)
		require.NoError(t, err)
		require.True(t, ok)
	}
}

func TestDoMarshalFailures(t *testing.T) {
	t.Parallel()

	t.Run("Request", func(t *testing.T) {
		t.Parallel()

		req := newRequest(testID)
		req.Parent = "\xff" // invalid UTF-8 in a proto3 string

		_, err := do(t, newGuard(t), testScope, req, &counter{})
		require.ErrorContains(t, err, "fingerprint")
	})

	t.Run("Response", func(t *testing.T) {
		t.Parallel()

		var storeErrs []error

		g := newGuard(t, requestid.WithStoreErrorHandler(func(_ context.Context, err error) { storeErrs = append(storeErrs, err) }))

		runs := 0
		h := func(context.Context, *v1.CreateBookRequest) (*v1.Book, error) {
			runs++
			return &v1.Book{Name: "\xff"}, nil
		}

		_, err := requestid.Do(t.Context(), g, testScope, newRequest(testID), h)
		require.NoError(t, err)
		require.Len(t, storeErrs, 1)

		_, err = requestid.Do(t.Context(), g, testScope, newRequest(testID), h)
		require.ErrorIs(t, err, requestid.ErrResponseNotRetained)
		assert.Equal(t, 1, runs, "the ID stays taken")
	})

	t.Run("Status", func(t *testing.T) {
		t.Parallel()

		var storeErrs []error

		g := newGuard(t,
			requestid.WithCacheError(func(error) *statuspb.Status { return &statuspb.Status{Code: 3, Message: "\xff"} }),
			requestid.WithStoreErrorHandler(func(_ context.Context, err error) { storeErrs = append(storeErrs, err) }))
		h := &counter{err: errors.New("boom")}

		for range 2 {
			_, err := do(t, g, testScope, newRequest(testID), h)
			require.ErrorIs(t, err, h.err)
		}

		assert.EqualValues(t, 2, h.runs.Load(), "an unstorable failure releases the ID")
		require.Len(t, storeErrs, 2)
	})
}
