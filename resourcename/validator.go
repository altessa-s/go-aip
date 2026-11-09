// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package resourcename

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// anyType is the resource_reference type meaning any resource type.
const anyType = "*"

// Validator checks client resource names against resource types: a
// registry of [Resource] types, the ID policy and the limits of its options.
// Register the types once at startup (from descriptors with
// [Validator.RegisterFile] or [Validator.RegisterFiles], or explicitly with
// [Validator.Register]), then check names from any goroutine.
//
// Every client error of a check is a [coreerrs.FieldError] on the field
// passed in (the request's JSON field name, such as "name" or "parent"),
// whose Err is a *[NameError] matching [ErrInvalidName] and its cause.
type Validator struct {
	opts validatorOptions

	mu    sync.RWMutex
	types map[string]*Resource
	plans map[protoreflect.MessageDescriptor]*requestPlan
}

// NewValidator returns a Validator without registered types.
func NewValidator(opts ...Option) *Validator {
	o := newValidatorOptions(opts...)
	if o.idValidator == nil {
		o.idValidator = PermissiveID
	}

	return &Validator{opts: *o, types: map[string]*Resource{}, plans: map[protoreflect.MessageDescriptor]*requestPlan{}}
}

// Register adds resource types. Registering a type again with the same
// patterns and name field is a no-op; with a different definition it is
// [ErrDuplicateType]. Either every type is registered or none.
func (v *Validator) Register(resources ...*Resource) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	return v.registerLocked(resources)
}

func (v *Validator) registerLocked(resources []*Resource) error {
	pending := make(map[string]*Resource, len(resources))

	for _, r := range resources {
		if r == nil {
			return fmt.Errorf("%w: nil resource", ErrInvalidResource)
		}

		prev, ok := v.types[r.typ]
		if !ok {
			prev, ok = pending[r.typ]
		}

		if ok && !sameDefinition(prev, r) {
			return fmt.Errorf("%w: %q", ErrDuplicateType, r.typ)
		}

		pending[r.typ] = r
	}

	for t, r := range pending {
		if _, ok := v.types[t]; !ok {
			v.types[t] = r
		}
	}

	clear(v.plans)

	return nil
}

// sameDefinition reports whether a and b define the same names.
func sameDefinition(a, b *Resource) bool {
	return a.nameField == b.nameField && slices.EqualFunc(a.patterns, b.patterns, func(x, y *Pattern) bool { return x.raw == y.raw })
}

// RegisterFile registers the resource types a file declares: its
// google.api.resource_definition options and the google.api.resource
// annotations of its messages, nested ones included. Either every type of
// the file is registered or none.
func (v *Validator) RegisterFile(fd protoreflect.FileDescriptor) error {
	resources, err := fileResources(fd)
	if err != nil {
		return err
	}

	return v.Register(resources...)
}

// RegisterFiles registers the resource types of every file of files
// (protoregistry.GlobalFiles when nil), file by file: the files that declare
// valid types are registered even when others fail, and the failures are
// returned joined.
func (v *Validator) RegisterFiles(files *protoregistry.Files) error {
	if files == nil {
		files = protoregistry.GlobalFiles
	}

	var errs []error

	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if err := v.RegisterFile(fd); err != nil {
			errs = append(errs, err)
		}

		return true
	})

	return errors.Join(errs...)
}

// fileResources collects the resource types fd declares.
func fileResources(fd protoreflect.FileDescriptor) ([]*Resource, error) {
	var out []*Resource

	if opts := fd.Options(); opts != nil && proto.HasExtension(opts, annotations.E_ResourceDefinition) {
		defs, _ := proto.GetExtension(opts, annotations.E_ResourceDefinition).([]*annotations.ResourceDescriptor)
		for _, rd := range defs {
			r, err := ResourceFromDescriptor(rd)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", fd.Path(), err)
			}

			out = append(out, r)
		}
	}

	var walk func(protoreflect.MessageDescriptors) error

	walk = func(mds protoreflect.MessageDescriptors) error {
		for i := range mds.Len() {
			md := mds.Get(i)
			if resourceDescriptor(md) != nil {
				r, err := ResourceOf(md)
				if err != nil {
					return err
				}

				out = append(out, r)
			}

			if err := walk(md.Messages()); err != nil {
				return err
			}
		}

		return nil
	}

	if err := walk(fd.Messages()); err != nil {
		return nil, err
	}

	return out, nil
}

// Lookup returns the registered resource type typ.
func (v *Validator) Lookup(typ string) (*Resource, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	r, ok := v.types[typ]

	return r, ok
}

// CheckName checks that name is a name of res: it matches one of its
// patterns, every ID passes the ID policy and the wildcard policy of the
// options, and the limits hold. An empty name is refused. Errors are a
// [coreerrs.FieldError] on field; a nil res is [ErrInvalidResource].
func (v *Validator) CheckName(field, name string, res *Resource, opts ...CheckOption) error {
	if res == nil {
		return fmt.Errorf("%w: nil resource", ErrInvalidResource)
	}

	return v.check(field, name, res.patterns, false, res.service, wildcardOf(opts), "")
}

// CheckParent checks that parent is the name of a parent of child, as the
// parent field of a List or Create of child: it matches one of the parent
// patterns of child (see [Resource.ParentPatterns]), with the same ID,
// wildcard and limit checks as [Validator.CheckName]. An empty parent is
// the root, valid only when child has a top-level pattern. Errors are a
// [coreerrs.FieldError] on field; a nil child is [ErrInvalidResource].
func (v *Validator) CheckParent(field, parent string, child *Resource, opts ...CheckOption) error {
	if child == nil {
		return fmt.Errorf("%w: nil resource", ErrInvalidResource)
	}

	return v.check(field, parent, child.parents, child.rootParent, child.service, wildcardOf(opts), child.parentLabel)
}

func wildcardOf(opts []CheckOption) WildcardPolicy {
	if len(opts) == 0 {
		return WildcardNone
	}

	return newCheckOptions(opts...).wildcard
}

// check is the shared check of a value against the patterns ps; root allows
// the empty value, what names the expectation in a mismatch.
func (v *Validator) check(field, value string, ps []*Pattern, root bool, service string, wildcard WildcardPolicy, what string) error {
	if ne := v.checkName(value, ps, root, service, wildcard, what); ne != nil {
		return &coreerrs.FieldError{Field: field, Reason: ne.Reason, Err: ne}
	}

	return nil
}

func (v *Validator) checkName(value string, ps []*Pattern, root bool, service string, wildcard WildcardPolicy, what string) *NameError {
	if v.opts.maxNameLength > 0 && len(value) > v.opts.maxNameLength {
		return nameError(ErrLimitExceeded, -1,
			fmt.Sprintf("the name is %d bytes long, more than the limit of %d", len(value), v.opts.maxNameLength))
	}

	if IsFullName(value) {
		if !v.opts.fullNames {
			return nameError(ErrInvalidFullName, -1, "full resource names are not accepted, use a relative name")
		}

		svc, rel, err := ParseFullName(value)
		if err != nil {
			ne, _ := errors.AsType[*NameError](err)

			return ne
		}

		if svc != service {
			return nameError(ErrInvalidFullName, -1, fmt.Sprintf("the service of the full resource name must be %q", service))
		}

		value = rel
	}

	if value == "" {
		if root {
			return nil
		}

		return nameError(ErrNoMatch, -1, "the name is empty")
	}

	if n := segmentCount(value); v.opts.maxSegments > 0 && n > v.opts.maxSegments {
		return nameError(ErrLimitExceeded, -1, fmt.Sprintf("the name has %d segments, more than the limit of %d", n, v.opts.maxSegments))
	}

	var buf [8]string

	p, vals := firstMatch(ps, buf[:0], value)
	if p == nil {
		if len(ps) == 0 {
			return nameError(ErrNoMatch, -1, what+" is the root, the name must be empty")
		}

		return noMatch(ps, value, what)
	}

	last := p.lastVariable()

	for i, id := range vals {
		if id == Wildcard {
			if wildcard == WildcardAny || (wildcard == WildcardAncestors && i != last) {
				continue
			}

			seg := p.variableSegment(i)

			return nameError(ErrWildcard, seg, fmt.Sprintf("segment %d, the %s ID, cannot be the \"-\" wildcard here", seg, p.vars[i]))
		}

		if err := v.opts.idValidator(id); err != nil {
			seg := p.variableSegment(i)

			return nameError(ErrInvalidID, seg, fmt.Sprintf("segment %d, the %s ID, %s", seg, p.vars[i], err.Error()))
		}
	}

	return nil
}

// ValidateRequest checks the resource names of a request message, as a
// handler does before any work:
//
//   - every top-level string field (singular or repeated) annotated with
//     google.api.resource_reference: a type reference must be a name of that
//     type ([Validator.CheckName]), a child_type reference the name of a
//     parent of that type ([Validator.CheckParent]); the wildcard policy of
//     opts applies to them. A reference to "*" (any type) is not checked;
//   - the name field of every top-level singular message field whose message
//     is a resource (the resource of a Create or Update), never with a
//     wildcard.
//
// Empty values are skipped: requiring a value is the job of
// google.api.field_behavior REQUIRED (package fieldbehavior). Field errors
// name the JSON field path ("name", "names[2]", "book.name"). A reference to
// a type that is not registered is [ErrUnknownType], a configuration error.
// The first invalid name is reported.
func (v *Validator) ValidateRequest(msg proto.Message, opts ...CheckOption) error {
	m := msg.ProtoReflect()
	if !m.IsValid() {
		return nil
	}

	pl := v.plan(m.Descriptor())
	if pl.err != nil {
		return pl.err
	}

	wildcard := wildcardOf(opts)

	for i := range pl.refs {
		if err := v.checkRef(m, &pl.refs[i], wildcard); err != nil {
			return err
		}
	}

	for _, n := range pl.nested {
		if !m.Has(n.fd) {
			continue
		}

		name := m.Get(n.fd).Message().Get(n.nameFd).String()
		if name == "" {
			continue
		}

		if err := v.check(n.field, name, n.res.patterns, false, n.res.service, WildcardNone, ""); err != nil {
			return err
		}
	}

	return nil
}

func (v *Validator) checkRef(m protoreflect.Message, r *refField, wildcard WildcardPolicy) error {
	if !m.Has(r.fd) {
		return nil
	}

	if !r.fd.IsList() {
		return v.checkRefValue(r, r.field, m.Get(r.fd).String(), wildcard)
	}

	list := m.Get(r.fd).List()
	for i := range list.Len() {
		if err := v.checkRefValue(r, r.field, list.Get(i).String(), wildcard); err != nil {
			return indexField(err, i)
		}
	}

	return nil
}

func (v *Validator) checkRefValue(r *refField, field, value string, wildcard WildcardPolicy) error {
	if value == "" {
		return nil
	}

	if r.parent {
		return v.check(field, value, r.res.parents, r.res.rootParent, r.res.service, wildcard, r.res.parentLabel)
	}

	return v.check(field, value, r.res.patterns, false, r.res.service, wildcard, "")
}

// indexField re-roots the field error of a repeated field's element under
// its index ("names" becomes "names[2]").
func indexField(err error, i int) error {
	if fe, ok := err.(*coreerrs.FieldError); ok { //nolint:errorlint // check returns the *FieldError itself
		fe.Field += "[" + strconv.Itoa(i) + "]"
	}

	return err
}

// requestPlan is what ValidateRequest checks in a request message type.
type requestPlan struct {
	refs   []refField
	nested []nestedField
	err    error
}

// refField is a string field annotated with google.api.resource_reference.
type refField struct {
	fd     protoreflect.FieldDescriptor
	field  string
	res    *Resource
	parent bool
}

// nestedField is a message field holding a resource.
type nestedField struct {
	fd     protoreflect.FieldDescriptor
	nameFd protoreflect.FieldDescriptor
	field  string
	res    *Resource
}

// plan returns the cached plan of md, building it on first use. Registering
// types drops the cache.
func (v *Validator) plan(md protoreflect.MessageDescriptor) *requestPlan {
	v.mu.RLock()
	pl, ok := v.plans[md]
	v.mu.RUnlock()

	if ok {
		return pl
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	if pl, ok = v.plans[md]; ok {
		return pl
	}

	pl = v.buildPlanLocked(md)
	v.plans[md] = pl

	return pl
}

func (v *Validator) buildPlanLocked(md protoreflect.MessageDescriptor) *requestPlan {
	pl := &requestPlan{}
	fields := md.Fields()

	for i := range fields.Len() {
		fd := fields.Get(i)

		switch {
		case fd.Kind() == protoreflect.StringKind && !fd.IsMap():
			ref, ok, err := v.refFieldLocked(fd)
			if err != nil {
				return &requestPlan{err: fmt.Errorf("%s: %w", md.FullName(), err)}
			}

			if ok {
				pl.refs = append(pl.refs, ref)
			}
		case fd.Message() != nil && fd.Cardinality() != protoreflect.Repeated:
			n, ok, err := v.nestedFieldLocked(fd)
			if err != nil {
				return &requestPlan{err: fmt.Errorf("%s: %w", md.FullName(), err)}
			}

			if ok {
				pl.nested = append(pl.nested, n)
			}
		}
	}

	return pl
}

// refFieldLocked returns the checked reference of fd, if it has one.
func (v *Validator) refFieldLocked(fd protoreflect.FieldDescriptor) (refField, bool, error) {
	opts := fd.Options()
	if opts == nil || !proto.HasExtension(opts, annotations.E_ResourceReference) {
		return refField{}, false, nil
	}

	rr, _ := proto.GetExtension(opts, annotations.E_ResourceReference).(*annotations.ResourceReference)

	typ, parent := rr.GetType(), false
	if typ == "" {
		typ, parent = rr.GetChildType(), true
	}

	if typ == "" || typ == anyType {
		return refField{}, false, nil
	}

	res, ok := v.types[typ]
	if !ok {
		return refField{}, false, fmt.Errorf("%w: field %s references %q", ErrUnknownType, fd.Name(), typ)
	}

	return refField{fd: fd, field: fd.JSONName(), res: res, parent: parent}, true, nil
}

// nestedFieldLocked returns the resource name check of fd, if its message
// is a resource.
func (v *Validator) nestedFieldLocked(fd protoreflect.FieldDescriptor) (nestedField, bool, error) {
	rd := resourceDescriptor(fd.Message())
	if rd == nil {
		return nestedField{}, false, nil
	}

	res, ok := v.types[rd.GetType()]
	if !ok {
		r, err := ResourceOf(fd.Message())
		if err != nil {
			return nestedField{}, false, err
		}

		res = r
	}

	nameFd := fd.Message().Fields().ByName(protoreflect.Name(res.nameField))
	if nameFd == nil || nameFd.Kind() != protoreflect.StringKind || nameFd.IsList() {
		return nestedField{}, false, fmt.Errorf("%w: %s: name field %q is not a singular string field",
			ErrInvalidResource, fd.Message().FullName(), res.nameField)
	}

	return nestedField{fd: fd, nameFd: nameFd, field: fd.JSONName() + "." + nameFd.JSONName(), res: res}, true, nil
}
