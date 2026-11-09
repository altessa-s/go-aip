// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package state

import (
	"fmt"
	"maps"
	"slices"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Enum is a generated protobuf state enum, such as Book_State.
type Enum interface {
	~int32
	protoreflect.Enum
}

// Machine is the transition table of a state enum: the transitions the
// resource's state-changing custom methods may perform. Only declared
// transitions are allowed, a state into itself included, so calling Publish
// on a PUBLISHED book fails as AIP-216 shows. A Machine is immutable and safe
// for concurrent use.
type Machine[S Enum] struct {
	targets map[S]map[S]struct{}
	desc    protoreflect.EnumDescriptor
	o       *options
}

// New returns the Machine of table, which maps each state to the states it
// may move to; a state with no targets, or one that appears only as a target,
// is terminal. Every state must be a value the enum declares other than its
// zero value (STATE_UNSPECIFIED, which AIP-216 says is never used), and the
// table must not be empty; otherwise the error matches [ErrInvalidTable].
//
//	machine := state.MustNew(map[pb.Book_State][]pb.Book_State{
//		pb.Book_DRAFT:     {pb.Book_PUBLISHED, pb.Book_DELETED},
//		pb.Book_PUBLISHED: {pb.Book_ARCHIVED},
//		pb.Book_ARCHIVED:  {pb.Book_PUBLISHED, pb.Book_DELETED},
//	})
func New[S Enum](table map[S][]S, opts ...Option) (*Machine[S], error) {
	var zero S

	m := &Machine[S]{targets: make(map[S]map[S]struct{}, len(table)), desc: zero.Descriptor(), o: newOptions(opts...)}
	if len(table) == 0 {
		return nil, fmt.Errorf("%w: the table has no state", ErrInvalidTable)
	}

	if m.o.field == "" {
		m.o.field = DefaultField
	}

	for _, from := range slices.Sorted(maps.Keys(table)) {
		if err := m.checkState(from); err != nil {
			return nil, err
		}

		if m.targets[from] == nil {
			m.targets[from] = make(map[S]struct{}, len(table[from]))
		}

		for _, to := range table[from] {
			if err := m.checkState(to); err != nil {
				return nil, err
			}

			m.targets[from][to] = struct{}{}
			if m.targets[to] == nil {
				m.targets[to] = make(map[S]struct{})
			}
		}
	}

	return m, nil
}

// MustNew is [New] that panics on an invalid table, for package-level
// machines.
func MustNew[S Enum](table map[S][]S, opts ...Option) *Machine[S] {
	m, err := New(table, opts...)
	if err != nil {
		panic(err)
	}

	return m
}

// checkState rejects the zero value and numbers the enum does not declare.
func (m *Machine[S]) checkState(s S) error {
	switch {
	case s == 0:
		return fmt.Errorf("%w: %s is the zero value, which must not be used", ErrInvalidTable, m.name(s))
	case m.desc.Values().ByNumber(protoreflect.EnumNumber(s)) == nil:
		return fmt.Errorf("%w: %d is not a value of %s", ErrInvalidTable, int32(s), m.desc.FullName())
	}

	return nil
}

// Allowed reports whether the table allows moving from one state to another.
func (m *Machine[S]) Allowed(from, to S) bool {
	_, ok := m.targets[from][to]
	return ok
}

// Check returns nil when the table allows moving from one state to another,
// and a *[TransitionError] otherwise, which matches
// [ErrTransitionNotAllowed] and [coreerrs.ErrFailedPrecondition]: AIP-216
// requires FAILED_PRECONDITION when the current state does not allow the
// transition.
func (m *Machine[S]) Check(from, to S) error {
	return m.check("", from, to)
}

// check is [Machine.Check] for a named method.
func (m *Machine[S]) check(method string, from, to S) error {
	if m.Allowed(from, to) {
		return nil
	}

	return &TransitionError{Method: method, From: m.name(from), To: m.name(to), Terminal: m.IsTerminal(from)}
}

// Targets returns the states the table allows moving to from a state, in
// enum number order; nil for a terminal or unknown state.
func (m *Machine[S]) Targets(from S) []S {
	if len(m.targets[from]) == 0 {
		return nil
	}

	return slices.Sorted(maps.Keys(m.targets[from]))
}

// IsTerminal reports whether a state of the table allows no transition.
// A state the table does not mention is not terminal, only unknown.
func (m *Machine[S]) IsTerminal(s S) bool {
	t, ok := m.targets[s]
	return ok && len(t) == 0
}

// States returns every state of the table in enum number order.
func (m *Machine[S]) States() []S {
	return slices.Sorted(maps.Keys(m.targets))
}

// Transition is the core of a state-changing custom method (AIP-216): it
// reads the state field of resource, the field [WithField] names, checks the
// move to the target state as [Machine.Check] does and sets the field to it.
// It returns the previous state. A refused move is a *[TransitionError] naming
// method (FAILED_PRECONDITION) and leaves the resource untouched. A resource
// without a singular state field of the Machine's enum is an error matching
// [ErrInvalidField].
//
//	func (s *Server) PublishBook(ctx context.Context, req *pb.PublishBookRequest) (*pb.Book, error) {
//		book, err := s.store.Get(ctx, req.GetName())
//		if err != nil { return nil, err }
//		if _, err := machine.Transition(book, "PublishBook", pb.Book_PUBLISHED); err != nil { return nil, err }
//		return s.store.Save(ctx, book)
//	}
func (m *Machine[S]) Transition(resource proto.Message, method string, to S) (S, error) {
	fd, err := m.field(resource)
	if err != nil {
		return 0, err
	}

	msg := resource.ProtoReflect()
	from := S(msg.Get(fd).Enum())

	if err := m.check(method, from, to); err != nil {
		return from, err
	}

	msg.Set(fd, protoreflect.ValueOfEnum(protoreflect.EnumNumber(to)))

	return from, nil
}

// field returns the state field of resource.
func (m *Machine[S]) field(resource proto.Message) (protoreflect.FieldDescriptor, error) {
	if resource == nil || !resource.ProtoReflect().IsValid() {
		return nil, fmt.Errorf("%w: the resource is nil", ErrInvalidField)
	}

	md := resource.ProtoReflect().Descriptor()

	fd := md.Fields().ByName(m.o.field)
	switch {
	case fd == nil:
		return nil, fmt.Errorf("%w: %s has no field %q", ErrInvalidField, md.FullName(), m.o.field)
	case fd.Kind() != protoreflect.EnumKind || fd.Cardinality() == protoreflect.Repeated:
		return nil, fmt.Errorf("%w: %s is not a singular enum", ErrInvalidField, fd.FullName())
	case fd.Enum().FullName() != m.desc.FullName():
		return nil, fmt.Errorf("%w: %s is a %s, not a %s", ErrInvalidField, fd.FullName(), fd.Enum().FullName(), m.desc.FullName())
	}

	return fd, nil
}

// name is the declared name of s, or its number when the enum lacks it.
func (m *Machine[S]) name(s S) string {
	return valueName(m.desc, protoreflect.EnumNumber(s))
}

// valueName is the declared name of n in ed, or n as a number.
func valueName(ed protoreflect.EnumDescriptor, n protoreflect.EnumNumber) string {
	if v := ed.Values().ByNumber(n); v != nil {
		return string(v.Name())
	}

	return fmt.Sprint(int32(n))
}
