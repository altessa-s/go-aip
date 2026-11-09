// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package requestid

//go:generate optgen generate --type=options --option-type=Option --output=options_gen.go
//go:generate optgen generate --type=memoryStoreOptions --option-type=MemoryStoreOption --output=memory_options_gen.go

import (
	"context"
	"time"

	"github.com/altessa-s/go-atlas-core/time/clock"

	statuspb "google.golang.org/genproto/googleapis/rpc/status"
)

// Defaults of a [Guard].
const (
	// DefaultField is the name of the request ID field (AIP-155).
	DefaultField = "request_id"
	// DefaultRetention is how long a completed request is replayed.
	DefaultRetention = 24 * time.Hour
	// DefaultLeaseTTL is how long a reservation lasts: a handler that runs
	// longer may run twice, so it must exceed the longest handler run.
	DefaultLeaseTTL = time.Minute
	// DefaultWaitTimeout is how long a duplicate waits for the first
	// request with its ID to finish before [ErrInProgress].
	DefaultWaitTimeout = 5 * time.Second
	// DefaultPollInterval is how often a waiting duplicate checks the store.
	DefaultPollInterval = 50 * time.Millisecond
	// DefaultMaxResponseSize is the largest serialized response stored, in
	// bytes.
	DefaultMaxResponseSize = 1 << 20
)

// defaultClock is the system clock.
func defaultClock() clock.Clock { return clock.Real() }

type options struct {
	// field is the name of the singular string request ID field of the
	// request messages, in its declared (snake_case) spelling.
	field string `optgen:"default=DefaultField"`
	// format is the accepted format of the request ID: FormatUUID (the
	// default), FormatUUID4 or FormatASCII.
	format Format
	// ignoredFields names the top-level request fields, by declared name,
	// that the fingerprint leaves out besides the request ID: values that
	// may differ between retries of the same request (a client trace ID,
	// say). A name a request does not declare is skipped.
	ignoredFields []string `optgen:"append" optval:"clone=eager"`
	// retention is how long a completed request is replayed to duplicates
	// (the AIP-155 timeframe for honoring request IDs).
	retention time.Duration `optgen:"default=DefaultRetention"`
	// leaseTTL is how long the reservation of a running request lasts. It
	// must exceed the longest handler run: once it expires a duplicate runs
	// the handler again.
	leaseTTL time.Duration `optgen:"default=DefaultLeaseTTL"`
	// waitTimeout is how long a duplicate of a running request waits for it
	// to finish before ErrInProgress. Zero fails at once; a negative value
	// keeps the default.
	waitTimeout time.Duration `optgen:"default=DefaultWaitTimeout" optval:"positive=allow_zero"`
	// pollInterval is how often a waiting duplicate checks the store.
	pollInterval time.Duration `optgen:"default=DefaultPollInterval"`
	// maxResponseSize bounds the serialized response stored, in bytes. A
	// larger response is returned to its caller but not stored: duplicates
	// get ErrResponseNotRetained. Zero is unlimited; a negative value keeps
	// the default.
	maxResponseSize int `optgen:"default=DefaultMaxResponseSize" optval:"positive=allow_zero"`
	// cacheError decides which handler failures are stored and replayed:
	// it returns the google.rpc.Status a duplicate fails with, or nil to
	// release the reservation so that a retry runs the handler again. By
	// default no failure is stored.
	cacheError func(error) *statuspb.Status
	// storeErrorHandler receives the store errors that do not fail the
	// call: a failed completion or release after the handler ran. Nil
	// drops them.
	storeErrorHandler func(context.Context, error)
	// clock is the time source of leases, retention and waits.
	clock clock.Clock `optgen:"default=defaultClock(),notnil"`
}

type memoryStoreOptions struct {
	// maxEntries bounds the live entries of the store. Zero is unlimited;
	// a negative value keeps the default.
	maxEntries int `optgen:"default=DefaultMaxEntries" optval:"positive=allow_zero"`
}
