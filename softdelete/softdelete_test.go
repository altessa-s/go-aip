// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package softdelete_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/altessa-s/go-atlas-core/time/clock"

	"github.com/altessa-s/go-aip/softdelete"

	sdtestv1 "github.com/altessa-s/go-aip/internal/proto/gen/softdeletetest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

var t0 = time.Date(2026, 11, 7, 12, 0, 0, 123456789, time.UTC)

func newPolicy(opts ...softdelete.Option) (*softdelete.Policy, *clock.Fake) {
	fake := clock.NewFake(t0)
	return softdelete.New(append([]softdelete.Option{softdelete.WithClock(fake)}, opts...)...), fake
}

func deletedBook(deleted, purge time.Time) *sdtestv1.Book {
	b := &sdtestv1.Book{Name: "books/1", DeleteTime: timestamppb.New(deleted)}
	if !purge.IsZero() {
		b.PurgeTime = timestamppb.New(purge)
	}

	return b
}

func TestDelete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		opts        []softdelete.Option
		current     proto.Message
		del         []softdelete.DeleteOption
		wantChanged bool
		wantErr     []error
		check       func(t *testing.T, msg proto.Message)
	}{
		{
			name:        "stamps delete and purge time",
			current:     &sdtestv1.Book{Name: "books/1"},
			wantChanged: true,
			check: func(t *testing.T, msg proto.Message) {
				t.Helper()

				b := msg.(*sdtestv1.Book)
				assert.True(t, b.GetDeleteTime().AsTime().Equal(t0))
				assert.True(t, b.GetPurgeTime().AsTime().Equal(t0.Add(softdelete.DefaultRetention)))
			},
		},
		{
			name:        "custom retention",
			opts:        []softdelete.Option{softdelete.WithRetention(time.Hour)},
			current:     &sdtestv1.Book{},
			wantChanged: true,
			check: func(t *testing.T, msg proto.Message) {
				t.Helper()
				assert.True(t, msg.(*sdtestv1.Book).GetPurgeTime().AsTime().Equal(t0.Add(time.Hour)))
			},
		},
		{
			name:        "zero retention clears purge time",
			opts:        []softdelete.Option{softdelete.WithRetention(0)},
			current:     &sdtestv1.Book{PurgeTime: timestamppb.New(t0)},
			wantChanged: true,
			check: func(t *testing.T, msg proto.Message) {
				t.Helper()
				assert.Nil(t, msg.(*sdtestv1.Book).GetPurgeTime())
			},
		},
		{
			name:        "negative retention keeps the default",
			opts:        []softdelete.Option{softdelete.WithRetention(-time.Hour)},
			current:     &sdtestv1.Book{},
			wantChanged: true,
			check: func(t *testing.T, msg proto.Message) {
				t.Helper()
				assert.True(t, msg.(*sdtestv1.Book).GetPurgeTime().AsTime().Equal(t0.Add(softdelete.DefaultRetention)))
			},
		},
		{
			name:        "expire_time as purge field",
			opts:        []softdelete.Option{softdelete.WithPurgeTimeField("expire_time")},
			current:     &sdtestv1.Shelf{},
			wantChanged: true,
			check: func(t *testing.T, msg proto.Message) {
				t.Helper()
				assert.True(t, msg.(*sdtestv1.Shelf).GetExpireTime().AsTime().Equal(t0.Add(softdelete.DefaultRetention)))
			},
		},
		{
			name:        "resource without purge field",
			current:     &sdtestv1.Shelf{},
			wantChanged: true,
			check: func(t *testing.T, msg proto.Message) {
				t.Helper()

				s := msg.(*sdtestv1.Shelf)
				assert.NotNil(t, s.GetDeleteTime())
				assert.Nil(t, s.GetExpireTime())
			},
		},
		{
			name:    "nil resource",
			current: (*sdtestv1.Book)(nil),
			wantErr: []error{softdelete.ErrNotFound, coreerrs.ErrNotFound},
		},
		{
			name:    "nil resource allow missing",
			current: nil,
			del:     []softdelete.DeleteOption{softdelete.WithAllowMissing(true)},
		},
		{
			name:    "already deleted",
			current: deletedBook(t0.Add(-time.Hour), t0.Add(time.Hour)),
			wantErr: []error{softdelete.ErrAlreadyDeleted, softdelete.ErrNotFound, coreerrs.ErrNotFound},
		},
		{
			name:    "already deleted allow missing",
			current: deletedBook(t0.Add(-time.Hour), t0.Add(time.Hour)),
			del:     []softdelete.DeleteOption{softdelete.WithAllowMissing(true)},
		},
		{
			name:    "purged",
			current: deletedBook(t0.Add(-2*time.Hour), t0.Add(-time.Hour)),
			wantErr: []error{softdelete.ErrNotFound, coreerrs.ErrNotFound},
		},
		{
			name:    "purged allow missing",
			current: deletedBook(t0.Add(-2*time.Hour), t0),
			del:     []softdelete.DeleteOption{softdelete.WithAllowMissing(true)},
		},
		{
			name:    "unsupported resource",
			current: &sdtestv1.Note{},
			del:     []softdelete.DeleteOption{softdelete.WithAllowMissing(true)},
			wantErr: []error{softdelete.ErrUnsupportedResource},
		},
		{
			name:    "precondition fails",
			current: &sdtestv1.Book{},
			del: []softdelete.DeleteOption{softdelete.WithPrecondition(func(proto.Message) error {
				return coreerrs.ErrConflict
			})},
			wantErr: []error{coreerrs.ErrConflict},
			check: func(t *testing.T, msg proto.Message) {
				t.Helper()
				assert.Nil(t, msg.(*sdtestv1.Book).GetDeleteTime())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, _ := newPolicy(tt.opts...)

			changed, err := p.Delete(tt.current, tt.del...)
			for _, want := range tt.wantErr {
				require.ErrorIs(t, err, want)
			}

			if len(tt.wantErr) == 0 {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.wantChanged, changed)

			if tt.check != nil {
				tt.check(t, tt.current)
			}
		})
	}
}

func TestDeleteSkipsPreconditionWhenMissing(t *testing.T) {
	t.Parallel()

	p, _ := newPolicy()
	called := false

	changed, err := p.Delete(deletedBook(t0, time.Time{}), softdelete.WithAllowMissing(true),
		softdelete.WithPrecondition(func(proto.Message) error { called = true; return nil }))
	require.NoError(t, err)
	assert.False(t, changed)
	assert.False(t, called)
}

func TestUndelete(t *testing.T) {
	t.Parallel()

	errConflict := coreerrs.ErrConflict

	tests := []struct {
		name         string
		current      proto.Message
		precondition softdelete.Precondition
		wantErr      []error
	}{
		{name: "restores", current: deletedBook(t0.Add(-time.Hour), t0.Add(time.Hour))},
		{name: "restores without purge time", current: deletedBook(t0.Add(-time.Hour), time.Time{})},
		{name: "nil", current: nil, wantErr: []error{softdelete.ErrNotFound, coreerrs.ErrNotFound}},
		{name: "invalid", current: (*sdtestv1.Book)(nil), wantErr: []error{softdelete.ErrNotFound}},
		{
			name:    "purged",
			current: deletedBook(t0.Add(-2*time.Hour), t0),
			wantErr: []error{softdelete.ErrNotFound, coreerrs.ErrNotFound},
		},
		{
			name:    "not deleted",
			current: &sdtestv1.Book{Name: "books/1"},
			wantErr: []error{softdelete.ErrNotDeleted, coreerrs.ErrAlreadyExists},
		},
		{
			name:         "precondition before state",
			current:      &sdtestv1.Book{Name: "books/1"},
			precondition: func(proto.Message) error { return errConflict },
			wantErr:      []error{errConflict},
		},
		{
			name:         "precondition passes",
			current:      deletedBook(t0, time.Time{}),
			precondition: func(proto.Message) error { return nil },
		},
		{name: "unsupported", current: &sdtestv1.Draft{}, wantErr: []error{softdelete.ErrUnsupportedResource}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, _ := newPolicy()

			err := p.Undelete(tt.current, tt.precondition)
			for _, want := range tt.wantErr {
				require.ErrorIs(t, err, want)
			}

			if len(tt.wantErr) != 0 {
				return
			}

			require.NoError(t, err)

			b := tt.current.(*sdtestv1.Book)
			assert.Nil(t, b.GetDeleteTime())
			assert.Nil(t, b.GetPurgeTime())
		})
	}
}

func TestUndeleteNotDeletedIsNotNotFound(t *testing.T) {
	t.Parallel()

	p, _ := newPolicy()
	err := p.Undelete(&sdtestv1.Book{}, nil)
	require.Error(t, err)
	assert.NotErrorIs(t, err, coreerrs.ErrNotFound)
}

func TestCheckGetAndVisibility(t *testing.T) {
	t.Parallel()

	live := &sdtestv1.Book{Name: "books/1"}
	deleted := deletedBook(t0.Add(-time.Hour), t0.Add(time.Hour))
	purged := deletedBook(t0.Add(-2*time.Hour), t0.Add(-time.Hour))
	deletedForever := deletedBook(t0.Add(-time.Hour), time.Time{})

	tests := []struct {
		name                 string
		msg                  proto.Message
		hide                 bool
		wantGet              error
		wantVisible          bool
		wantVisibleShow      bool
		wantDeleted, wantDue bool
	}{
		{name: "live", msg: live, wantVisible: true, wantVisibleShow: true},
		{name: "deleted", msg: deleted, wantVisibleShow: true, wantDeleted: true},
		{name: "deleted without purge time", msg: deletedForever, wantVisibleShow: true, wantDeleted: true},
		{name: "deleted hidden on get", msg: deleted, hide: true, wantGet: softdelete.ErrNotFound, wantVisibleShow: true, wantDeleted: true},
		{name: "purged", msg: purged, wantGet: softdelete.ErrNotFound, wantDeleted: true, wantDue: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, _ := newPolicy(softdelete.WithHideDeletedOnGet(tt.hide))

			err := p.CheckGet(tt.msg)
			if tt.wantGet != nil {
				require.ErrorIs(t, err, tt.wantGet)
				require.ErrorIs(t, err, coreerrs.ErrNotFound)
			} else {
				require.NoError(t, err)
			}

			visible, err := p.Visible(tt.msg, false)
			require.NoError(t, err)
			assert.Equal(t, tt.wantVisible, visible)

			visible, err = p.Visible(tt.msg, true)
			require.NoError(t, err)
			assert.Equal(t, tt.wantVisibleShow, visible)

			deleted, err := p.IsDeleted(tt.msg)
			require.NoError(t, err)
			assert.Equal(t, tt.wantDeleted, deleted)

			due, err := p.PurgeDue(tt.msg)
			require.NoError(t, err)
			assert.Equal(t, tt.wantDue, due)
		})
	}
}

func TestNilAndUnsupportedQueries(t *testing.T) {
	t.Parallel()

	p, _ := newPolicy()

	for _, msg := range []proto.Message{nil, &sdtestv1.Note{}} {
		require.Error(t, p.CheckGet(msg))

		_, err := p.Visible(msg, true)
		require.Error(t, err)

		_, err = p.IsDeleted(msg)
		require.Error(t, err)

		_, err = p.PurgeDue(msg)
		require.Error(t, err)
	}
}

func TestPurgeTimeFollowsClock(t *testing.T) {
	t.Parallel()

	p, fake := newPolicy(softdelete.WithRetention(time.Hour))
	b := &sdtestv1.Book{}

	_, err := p.Delete(b)
	require.NoError(t, err)

	due, err := p.PurgeDue(b)
	require.NoError(t, err)
	assert.False(t, due)

	fake.Advance(time.Hour)

	due, err = p.PurgeDue(b)
	require.NoError(t, err)
	assert.True(t, due)
	require.ErrorIs(t, p.Undelete(b, nil), softdelete.ErrNotFound)
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    []softdelete.Option
		msg     proto.Message
		wantErr bool
	}{
		{name: "book", msg: &sdtestv1.Book{}},
		{name: "shelf without purge time", msg: &sdtestv1.Shelf{}},
		{name: "json field name", opts: []softdelete.Option{softdelete.WithDeleteTimeField("deleteTime")}, msg: &sdtestv1.Book{}},
		{name: "blank field name keeps default", opts: []softdelete.Option{softdelete.WithDeleteTimeField("  ")}, msg: &sdtestv1.Book{}},
		{name: "no delete time", msg: &sdtestv1.Note{}, wantErr: true},
		{name: "not output only", msg: &sdtestv1.Draft{}, wantErr: true},
		{name: "output only waived", opts: []softdelete.Option{softdelete.WithOutputOnlyNotRequired(true)}, msg: &sdtestv1.Draft{}},
		{name: "wrong type", msg: &sdtestv1.Scroll{}, wantErr: true},
		{name: "repeated", opts: []softdelete.Option{softdelete.WithDeleteTimeField("purge_time")}, msg: &sdtestv1.Scroll{}, wantErr: true},
		{name: "invalid purge field", opts: []softdelete.Option{softdelete.WithPurgeTimeField("title")}, msg: &sdtestv1.Book{}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, _ := newPolicy(tt.opts...)

			err := p.Validate(tt.msg.ProtoReflect().Descriptor())
			if tt.wantErr {
				require.ErrorIs(t, err, softdelete.ErrUnsupportedResource)
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestNilClockKeepsSystemClock(t *testing.T) {
	t.Parallel()

	p := softdelete.New(softdelete.WithClock(nil))
	b := &sdtestv1.Book{}

	before := time.Now()
	_, err := p.Delete(b)
	require.NoError(t, err)
	assert.False(t, b.GetDeleteTime().AsTime().Before(before.Truncate(time.Second)))
}

// TestDynamicMessage covers a dynamicpb resource whose descriptor shares its
// full name with the generated Book: the cache must not mix their fields.
func TestDynamicMessage(t *testing.T) {
	t.Parallel()

	fdp := protodesc.ToFileDescriptorProto(sdtestv1.File_softdeletetest_v1_test_proto)
	fdp.Name = new("softdeletetest/v1/dynamic.proto")

	fd, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	require.NoError(t, err)

	md := fd.Messages().ByName("Book")
	p, _ := newPolicy()

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			_, err := p.Delete(&sdtestv1.Book{})
			assert.NoError(t, err)

			dyn := dynamicpb.NewMessage(md)
			_, err = p.Delete(dyn)
			assert.NoError(t, err)
			assert.True(t, dyn.Has(md.Fields().ByName("delete_time")))
			assert.True(t, dyn.Has(md.Fields().ByName("purge_time")))
			assert.NoError(t, p.Undelete(dyn, nil))
			assert.False(t, dyn.Has(md.Fields().ByName("delete_time")))
		})
	}

	wg.Wait()
}

func TestErrorsAreDistinct(t *testing.T) {
	t.Parallel()

	assert.False(t, errors.Is(softdelete.ErrNotFound, softdelete.ErrAlreadyDeleted))
	assert.False(t, errors.Is(softdelete.ErrNotDeleted, coreerrs.ErrNotFound))
	assert.NotEmpty(t, softdelete.ErrNotDeleted.Error())
}
