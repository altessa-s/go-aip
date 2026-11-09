// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package requestid

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
)

// Scope isolates request IDs: the same ID sent by two principals, or to two
// methods, names two different requests. Both parts go into the store key
// only as a hash.
type Scope struct {
	// Method is the full name of the RPC method, such as
	// "/library.v1.LibraryService/CreateBook". It is required.
	Method string
	// Principal identifies the caller: the authenticated user, service
	// account or tenant, or a combination of them ("tenants/t1/users/u2").
	// Empty puts every caller in one scope, so that one caller can replay
	// the response of another; leave it empty only for an unauthenticated,
	// single-tenant API.
	Principal string
}

// Handler runs a request.
type Handler[Req, Resp proto.Message] func(ctx context.Context, req Req) (Resp, error)

// Guard deduplicates requests by their AIP-155 request ID. It is safe for
// concurrent use; one Guard serves every method of a service.
type Guard struct {
	store Store
	o     *options
}

// New returns a [Guard] keeping its entries in store.
func New(store Store, opts ...Option) (*Guard, error) {
	if nilcheck.IsNil(store) {
		return nil, errors.New("requestid: store is nil")
	}

	o := newOptions(opts...)
	if !o.format.valid() {
		return nil, fmt.Errorf("requestid: unknown %v", o.format)
	}

	return &Guard{store: store, o: o}, nil
}

// Do runs handler on req at most once per request ID within scope.
//
//   - An empty request ID runs handler without deduplication.
//   - An invalid one fails with a *[coreerrs.FieldError] on the request ID
//     field (its JSON name) holding [ErrInvalidRequestID].
//   - The first request with an ID reserves it and runs handler. A success
//     is stored for the retention; a failure releases the reservation
//     unless [WithCacheError] stores it.
//   - A duplicate with the same parameters gets the stored response, or a
//     *[ReplayedError] for a stored failure, without running handler. It
//     waits up to [WithWaitTimeout] for a request still running, then fails
//     with [ErrInProgress].
//   - A request reusing the ID with other parameters fails with
//     [ErrRequestIDReused].
//
// The store is consulted before handler runs: when it fails, Do fails with
// [ErrStore] and handler does not run. Once handler has run, its result is
// returned even if storing it fails (see [WithStoreErrorHandler]); the
// outcome is stored with a context detached from the cancellation of ctx.
func Do[Req, Resp proto.Message](ctx context.Context, g *Guard, scope Scope, req Req, handler Handler[Req, Resp]) (Resp, error) {
	var zero Resp

	m := req.ProtoReflect()
	if !m.IsValid() {
		return zero, fmt.Errorf("%w: requestid: request is nil", coreerrs.ErrInvalidArgument)
	}

	fd, err := g.field(m.Descriptor())
	if err != nil {
		return zero, err
	}

	id, err := Canonical(m.Get(fd).String(), g.o.format)
	if err != nil {
		return zero, &coreerrs.FieldError{Field: fd.JSONName(), Reason: err.Error(), Err: err}
	}

	if id == "" {
		return handler(ctx, req)
	}

	if scope.Method == "" {
		return zero, ErrInvalidScope
	}

	fp, err := g.fingerprint(m, fd)
	if err != nil {
		return zero, err
	}

	c := call[Req, Resp]{g: g, key: storeKey(scope, id), fp: fp, req: req, handler: handler}

	return c.run(ctx)
}

// field returns the request ID field of md.
func (g *Guard) field(md protoreflect.MessageDescriptor) (protoreflect.FieldDescriptor, error) {
	fd := md.Fields().ByName(protoreflect.Name(g.o.field))
	if fd == nil || fd.Kind() != protoreflect.StringKind || fd.Cardinality() == protoreflect.Repeated {
		return nil, fmt.Errorf("%w: %s has no singular string field %q", ErrNoRequestIDField, md.FullName(), g.o.field)
	}

	return fd, nil
}

// fingerprint hashes the message type and the deterministic encoding of m
// without its request ID and ignored fields.
//
// The encoded message is a shallow copy of m: a new message of the same type
// holding m's populated fields, except the excluded ones, and its unknown
// fields. Sub-messages, lists and maps are shared with m, read-only: marshaling
// does not modify them. The encoding is the one a deep clone with the excluded
// fields cleared would produce.
func (g *Guard) fingerprint(m protoreflect.Message, idField protoreflect.FieldDescriptor) ([]byte, error) {
	c := m.New()

	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd != idField && !g.ignored(fd) {
			c.Set(fd, v)
		}

		return true
	})

	if unknown := m.GetUnknown(); len(unknown) > 0 {
		c.SetUnknown(unknown)
	}

	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(c.Interface())
	if err != nil {
		return nil, fmt.Errorf("requestid: fingerprint: %w", err)
	}

	h := sha256.New()
	h.Write([]byte(m.Descriptor().FullName()))
	h.Write([]byte{0})
	h.Write(b)

	return h.Sum(nil), nil
}

// ignored reports whether fd is a top-level field named by
// [WithIgnoredFields]. Extensions are never ignored.
func (g *Guard) ignored(fd protoreflect.FieldDescriptor) bool {
	if fd.IsExtension() {
		return false
	}

	return slices.Contains(g.o.ignoredFields, string(fd.Name()))
}

// storeKey hashes the scope and the canonical ID into a fixed-length key
// that holds no caller data. Each part is length-prefixed, so no two
// distinct (method, principal, id) triples share a key.
func storeKey(scope Scope, id string) string {
	h := sha256.New()
	h.Write([]byte("requestid/v1"))

	var n [binary.MaxVarintLen64]byte

	for _, part := range [...]string{scope.Method, scope.Principal, id} {
		h.Write(binary.AppendUvarint(n[:0], uint64(len(part))))
		h.Write([]byte(part))
	}

	return hex.EncodeToString(h.Sum(nil))
}

// newToken returns a random reservation token.
func newToken() string { return rand.Text() }

// call is one deduplicated request.
type call[Req, Resp proto.Message] struct {
	g       *Guard
	key     string
	fp      []byte
	req     Req
	handler Handler[Req, Resp]
}

func (c *call[Req, Resp]) run(ctx context.Context) (Resp, error) {
	var zero Resp

	o := c.g.o
	deadline := o.clock.Now().Add(o.waitTimeout)

	for {
		now := o.clock.Now()
		token := newToken()
		pending := Entry{State: StatePending, Token: token, Fingerprint: c.fp, ExpiresAt: now.Add(o.leaseTTL)}

		cur, reserved, err := c.g.store.Reserve(ctx, c.key, pending, now)
		if err != nil {
			return zero, coreerrs.JoinWrap(ErrStore, err)
		}

		if reserved {
			return c.execute(ctx, token)
		}

		if !bytes.Equal(cur.Fingerprint, c.fp) {
			return zero, ErrRequestIDReused
		}

		if cur.State == StateCompleted {
			return c.replay(cur)
		}

		if !now.Before(deadline) {
			return zero, ErrInProgress
		}

		if err := o.clock.Sleep(ctx, min(o.pollInterval, deadline.Sub(now))); err != nil {
			return zero, err
		}
	}
}

// execute runs the handler under the reservation token and stores its
// outcome.
func (c *call[Req, Resp]) execute(ctx context.Context, token string) (resp Resp, err error) {
	o := c.g.o
	detached := context.WithoutCancel(ctx)
	settled := false

	defer func() {
		if !settled {
			// The handler panicked: free the ID for a retry.
			c.storeErr(detached, c.g.store.Release(detached, c.key, token, o.clock.Now()))
		}
	}()

	resp, err = c.handler(ctx, c.req)
	settled = true

	if err != nil {
		c.fail(detached, token, err)
		return resp, err
	}

	done := Entry{State: StateCompleted, Fingerprint: c.fp}

	b, merr := proto.Marshal(resp)

	switch {
	case merr != nil:
		// Unreachable for generated messages; keep the ID taken so that a
		// duplicate never runs the handler twice.
		c.storeErr(detached, fmt.Errorf("requestid: marshal response: %w", merr))

		done.ResponseOmitted = true
	case o.maxResponseSize > 0 && len(b) > o.maxResponseSize:
		done.ResponseOmitted = true
	default:
		done.Response = b
	}

	c.complete(detached, token, done)

	return resp, nil
}

// fail stores the failure err when cacheError selects it, and releases the
// reservation otherwise.
func (c *call[Req, Resp]) fail(ctx context.Context, token string, err error) {
	o := c.g.o

	var st *statuspb.Status
	if o.cacheError != nil {
		st = o.cacheError(err)
	}

	if st.GetCode() == 0 { // nil or OK: not a failure to replay
		c.storeErr(ctx, c.g.store.Release(ctx, c.key, token, o.clock.Now()))
		return
	}

	b, merr := proto.Marshal(st)
	if merr != nil {
		c.storeErr(ctx, fmt.Errorf("requestid: marshal status: %w", merr))
		c.storeErr(ctx, c.g.store.Release(ctx, c.key, token, o.clock.Now()))

		return
	}

	c.complete(ctx, token, Entry{State: StateCompleted, Fingerprint: c.fp, Failure: b})
}

func (c *call[Req, Resp]) complete(ctx context.Context, token string, done Entry) {
	now := c.g.o.clock.Now()
	done.Token = token
	done.ExpiresAt = now.Add(c.g.o.retention)
	c.storeErr(ctx, c.g.store.Complete(ctx, c.key, token, done, now))
}

func (c *call[Req, Resp]) storeErr(ctx context.Context, err error) {
	if err != nil && c.g.o.storeErrorHandler != nil {
		c.g.o.storeErrorHandler(ctx, err)
	}
}

// replay returns the stored outcome of a completed request.
func (c *call[Req, Resp]) replay(e Entry) (Resp, error) {
	var zero Resp

	switch {
	case len(e.Failure) > 0:
		st := new(statuspb.Status)
		if err := proto.Unmarshal(e.Failure, st); err != nil {
			return zero, coreerrs.JoinWrap(ErrStore, fmt.Errorf("requestid: decode stored failure: %w", err))
		}

		return zero, &ReplayedError{Status: st}
	case e.ResponseOmitted:
		return zero, ErrResponseNotRetained
	}

	if any(zero) == nil {
		return zero, errors.New("requestid: the response type must be a concrete message type")
	}

	resp, ok := zero.ProtoReflect().Type().New().Interface().(Resp)
	if !ok {
		return zero, fmt.Errorf("requestid: cannot instantiate %T", zero)
	}

	if err := proto.Unmarshal(e.Response, resp); err != nil {
		return zero, coreerrs.JoinWrap(ErrStore, fmt.Errorf("requestid: decode stored response: %w", err))
	}

	return resp, nil
}
