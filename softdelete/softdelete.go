// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package softdelete

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-atlas-core/time/clock"
	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/internal/behavior"
)

// timestampName is the full name of google.protobuf.Timestamp.
const timestampName protoreflect.FullName = "google.protobuf.Timestamp"

// Field numbers of google.protobuf.Timestamp.
const (
	timestampSeconds protoreflect.FieldNumber = 1
	timestampNanos   protoreflect.FieldNumber = 2
)

// Precondition checks the stored resource before a state change, an AIP-154
// etag comparison say. Its error is returned to the caller unchanged.
type Precondition func(current proto.Message) error

// Policy applies AIP-164 soft delete to protobuf resources through their
// descriptors: Delete stamps delete_time and purge_time, Undelete clears
// them, and Get and List decide what a soft-deleted resource looks like to a
// client. It is immutable, caches the resolved fields of each message type
// and is safe for concurrent use.
type Policy struct {
	opts   *policyOptions
	fields sync.Map // protoreflect.MessageDescriptor -> *resourceFields
}

// resourceFields are the soft-delete fields of one message type.
type resourceFields struct {
	deleteTime protoreflect.FieldDescriptor
	purgeTime  protoreflect.FieldDescriptor // nil when the resource has none
}

// New returns a soft-delete policy.
func New(opts ...Option) *Policy {
	o := newPolicyOptions(opts...)
	if nilcheck.IsNil(o.clock) {
		o.clock = clock.Real()
	}

	return &Policy{opts: o}
}

// Validate checks that the resource type md supports soft delete: a singular
// google.protobuf.Timestamp delete time field and, when present, purge time
// field, both OUTPUT_ONLY (AIP-148) unless [WithOutputOnlyNotRequired]. A
// failure matches [ErrUnsupportedResource]. Call it at startup for every
// soft-deletable resource; the other methods return the same error.
func (p *Policy) Validate(md protoreflect.MessageDescriptor) error {
	_, err := p.resolve(md)
	return err
}

// Delete soft-deletes current, the stored resource, in place (AIP-164): it
// sets delete_time to now and purge_time to now plus the retention (cleared
// when the retention is zero), and reports whether it changed current. The
// caller then saves it.
//
// A nil current is a missing resource and a resource past its purge time is
// treated as one. A missing or already soft-deleted resource is
// [ErrNotFound] or [ErrAlreadyDeleted] (NOT_FOUND), or no change without an
// error under [WithAllowMissing]; the precondition is not checked then. Else
// the [WithPrecondition] check runs before the resource changes.
func (p *Policy) Delete(current proto.Message, opts ...DeleteOption) (bool, error) {
	o := newDeleteOptions(opts...)

	m, f, err := p.reflect(current)
	if err != nil {
		if o.allowMissing && errors.Is(err, ErrNotFound) {
			return false, nil
		}

		return false, err
	}

	now := p.opts.clock.Now()

	if purged := f.purgeDue(m, now); purged || m.Has(f.deleteTime) {
		switch {
		case o.allowMissing:
			return false, nil
		case purged:
			return false, ErrNotFound
		default:
			return false, ErrAlreadyDeleted
		}
	}

	if o.precondition != nil {
		if err = o.precondition(current); err != nil {
			return false, err
		}
	}

	setTime(m, f.deleteTime, now)

	if f.purgeTime != nil {
		if p.opts.retention > 0 {
			setTime(m, f.purgeTime, now.Add(p.opts.retention))
		} else {
			m.Clear(f.purgeTime)
		}
	}

	return true, nil
}

// Undelete restores current, the stored resource, in place (AIP-164): it
// clears delete_time and purge_time. The caller then saves it and returns
// the resource.
//
// The checks run in order: a nil current, or a resource past its purge time,
// is [ErrNotFound] (NOT_FOUND: never created or already purged); then the
// precondition, when non-nil, runs (an AIP-154 etag check); then a resource
// that is not soft-deleted is [ErrNotDeleted] (ALREADY_EXISTS).
func (p *Policy) Undelete(current proto.Message, precondition Precondition) error {
	m, f, err := p.reflect(current)
	if err != nil {
		return err
	}

	if f.purgeDue(m, p.opts.clock.Now()) {
		return ErrNotFound
	}

	if precondition != nil {
		if err = precondition(current); err != nil {
			return err
		}
	}

	if !m.Has(f.deleteTime) {
		return ErrNotDeleted
	}

	m.Clear(f.deleteTime)

	if f.purgeTime != nil {
		m.Clear(f.purgeTime)
	}

	return nil
}

// CheckGet decides whether Get returns current, the stored resource: a nil
// current or a resource past its purge time is [ErrNotFound]. A soft-deleted
// resource is returned, as AIP-164 recommends, unless
// [WithHideDeletedOnGet] makes it [ErrNotFound] too.
func (p *Policy) CheckGet(current proto.Message) error {
	m, f, err := p.reflect(current)
	if err != nil {
		return err
	}

	if f.purgeDue(m, p.opts.clock.Now()) || (p.opts.hideDeletedOnGet && m.Has(f.deleteTime)) {
		return ErrNotFound
	}

	return nil
}

// IsDeleted reports whether current is soft-deleted: its delete_time is set.
// A nil current is [ErrNotFound].
func (p *Policy) IsDeleted(current proto.Message) (bool, error) {
	m, f, err := p.reflect(current)
	if err != nil {
		return false, err
	}

	return m.Has(f.deleteTime), nil
}

// PurgeDue reports whether current is soft-deleted and its purge time has
// passed, so an expunge job may remove it for good. A nil current is
// [ErrNotFound].
func (p *Policy) PurgeDue(current proto.Message) (bool, error) {
	m, f, err := p.reflect(current)
	if err != nil {
		return false, err
	}

	return f.purgeDue(m, p.opts.clock.Now()), nil
}

// Visible reports whether a List returns current (AIP-164): a soft-deleted
// resource only when showDeleted is true, a resource past its purge time
// never. A nil current is [ErrNotFound]. Use it on items a list reads from
// memory; [Policy.Restrict] builds the same rule as a filter for a store.
func (p *Policy) Visible(current proto.Message, showDeleted bool) (bool, error) {
	m, f, err := p.reflect(current)
	if err != nil {
		return false, err
	}

	if f.purgeDue(m, p.opts.clock.Now()) {
		return false, nil
	}

	return showDeleted || !m.Has(f.deleteTime), nil
}

// reflect returns the reflection of msg and its soft-delete fields; a nil or
// invalid msg is [ErrNotFound].
func (p *Policy) reflect(msg proto.Message) (protoreflect.Message, *resourceFields, error) {
	if nilcheck.IsNil(msg) {
		return nil, nil, ErrNotFound
	}

	m := msg.ProtoReflect()
	if !m.IsValid() {
		return nil, nil, ErrNotFound
	}

	f, err := p.resolve(m.Descriptor())
	if err != nil {
		return nil, nil, err
	}

	return m, f, nil
}

// resolve returns the cached soft-delete fields of md.
func (p *Policy) resolve(md protoreflect.MessageDescriptor) (*resourceFields, error) {
	if cached, ok := p.fields.Load(md); ok {
		return cached.(*resourceFields), nil //nolint:errcheck,forcetypeassert // only *resourceFields is stored
	}

	del, err := p.timeField(md, p.opts.deleteTimeField)
	if err != nil {
		return nil, err
	}

	if del == nil {
		return nil, fmt.Errorf("%w: %s has no field %q", ErrUnsupportedResource, md.FullName(), p.opts.deleteTimeField)
	}

	purge, err := p.timeField(md, p.opts.purgeTimeField)
	if err != nil {
		return nil, err
	}

	f := &resourceFields{deleteTime: del, purgeTime: purge}
	p.fields.Store(md, f)

	return f, nil
}

// timeField returns the field name of md, nil when md has none, or an error
// when it is not a singular OUTPUT_ONLY google.protobuf.Timestamp.
func (p *Policy) timeField(md protoreflect.MessageDescriptor, name string) (protoreflect.FieldDescriptor, error) {
	fd := md.Fields().ByName(protoreflect.Name(name))
	if fd == nil {
		fd = md.Fields().ByJSONName(name)
	}

	if fd == nil {
		return nil, nil //nolint:nilnil // an absent field is not an error
	}

	if fd.Cardinality() == protoreflect.Repeated || fd.Message() == nil || fd.Message().FullName() != timestampName {
		return nil, fmt.Errorf("%w: %s.%s is not a singular google.protobuf.Timestamp", ErrUnsupportedResource, md.FullName(), fd.Name())
	}

	if !p.opts.outputOnlyNotRequired && !behavior.Has(fd, annotations.FieldBehavior_OUTPUT_ONLY) {
		return nil, fmt.Errorf("%w: %s.%s is not OUTPUT_ONLY", ErrUnsupportedResource, md.FullName(), fd.Name())
	}

	return fd, nil
}

// purgeDue reports whether m is soft-deleted and its purge time is not
// after now.
func (f *resourceFields) purgeDue(m protoreflect.Message, now time.Time) bool {
	if f.purgeTime == nil || !m.Has(f.deleteTime) || !m.Has(f.purgeTime) {
		return false
	}

	return !now.Before(getTime(m, f.purgeTime))
}

// setTime sets the Timestamp field fd of m to t through reflection, so it
// works for generated and dynamic messages alike.
func setTime(m protoreflect.Message, fd protoreflect.FieldDescriptor, t time.Time) {
	ts := m.NewField(fd).Message()
	fields := ts.Descriptor().Fields()
	ts.Set(fields.ByNumber(timestampSeconds), protoreflect.ValueOfInt64(t.Unix()))
	ts.Set(fields.ByNumber(timestampNanos), protoreflect.ValueOfInt32(int32(t.Nanosecond()))) //nolint:gosec // nanoseconds fit int32
	m.Set(fd, protoreflect.ValueOfMessage(ts))
}

// getTime reads the Timestamp field fd of m.
func getTime(m protoreflect.Message, fd protoreflect.FieldDescriptor) time.Time {
	ts := m.Get(fd).Message()
	fields := ts.Descriptor().Fields()

	return time.Unix(ts.Get(fields.ByNumber(timestampSeconds)).Int(), ts.Get(fields.ByNumber(timestampNanos)).Int())
}
