// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldbehavior_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldbehavior"

	testpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// validAccount returns an Account whose every REQUIRED field is set.
func validAccount() *testpb.Account {
	return &testpb.Account{
		Name:        "accounts/1",
		DisplayName: "Acme",
		Key:         []byte{1},
		Enabled:     true,
		State:       testpb.State_ACTIVE,
		Quota:       new(int32(5)),
		Owner:       &testpb.Owner{Email: "o@example.com", Org: "org-1"},
		Emails:      []string{"a@example.com"},
		Attributes:  map[string]string{"k": "v"},
		Billing:     &testpb.Account_Card{Card: "4242"},
		Region:      "eu",
		UpdateTime:  "server",
	}
}

func violationPaths(t *testing.T, err error) []string {
	t.Helper()

	vErr, ok := errors.AsType[*fieldbehavior.BehaviorViolationError](err)
	require.True(t, ok, "want *BehaviorViolationError, got %v", err)
	require.ErrorIs(t, err, fieldbehavior.ErrBehaviorViolation)

	paths := make([]string, 0, len(vErr.Violations))
	for _, v := range vErr.Violations {
		require.NotEmpty(t, v.Reason, v.Path)
		paths = append(paths, v.Path)
	}

	return paths
}

// TestValidateRequired pins the AIP-203 REQUIRED rule: a REQUIRED field must
// be present and set to a non-empty value; nested messages are checked only
// when present; a REQUIRED oneof member is satisfied by any member.
func TestValidateRequired(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*testpb.Account)
		want   []string
	}{
		{name: "every required field set", mutate: func(*testpb.Account) {}},
		{name: "empty string", mutate: func(a *testpb.Account) { a.DisplayName = "" }, want: []string{"display_name"}},
		{name: "empty bytes", mutate: func(a *testpb.Account) { a.Key = []byte{} }, want: []string{"key"}},
		{name: "false bool", mutate: func(a *testpb.Account) { a.Enabled = false }, want: []string{"enabled"}},
		{name: "zero enum", mutate: func(a *testpb.Account) { a.State = testpb.State_STATE_UNSPECIFIED }, want: []string{"state"}},
		{name: "explicit-presence zero", mutate: func(a *testpb.Account) { a.Quota = new(int32(0)) }, want: []string{"quota"}},
		{name: "explicit-presence unset", mutate: func(a *testpb.Account) { a.Quota = nil }, want: []string{"quota"}},
		{name: "unset message", mutate: func(a *testpb.Account) { a.Owner = nil }, want: []string{"owner"}},
		{name: "empty list", mutate: func(a *testpb.Account) { a.Emails = nil }, want: []string{"emails"}},
		{name: "empty map", mutate: func(a *testpb.Account) { a.Attributes = map[string]string{} }, want: []string{"attributes"}},
		{name: "required field of a present message", mutate: func(a *testpb.Account) { a.Owner.Email = "" }, want: []string{"owner.email"}},
		{name: "absent optional message is not descended", mutate: func(a *testpb.Account) { a.BackupOwner = nil }},
		{
			name:   "present optional message is checked",
			mutate: func(a *testpb.Account) { a.BackupOwner = &testpb.Owner{Note: "n"} },
			want:   []string{"backup_owner.email"},
		},
		{
			name: "repeated elements and map values",
			mutate: func(a *testpb.Account) {
				a.Members = []*testpb.Owner{{Email: "m@example.com"}, {Note: "no email"}}
				a.Delegates = map[string]*testpb.Owner{"b": {}, "a": {Email: "d@example.com"}, "c.d": {}}
			},
			want: []string{"members.1.email", "delegates.b.email", "delegates.`c.d`.email"},
		},
		{name: "oneof without a member", mutate: func(a *testpb.Account) { a.Billing = nil }, want: []string{"card"}},
		{name: "oneof holding another member", mutate: func(a *testpb.Account) { a.Billing = &testpb.Account_InvoiceEmail{InvoiceEmail: "i@x"} }},
		{name: "oneof holding the member empty", mutate: func(a *testpb.Account) { a.Billing = &testpb.Account_Card{} }, want: []string{"card"}},
		{
			name:   "several violations in declaration order",
			mutate: func(a *testpb.Account) { a.Owner, a.DisplayName, a.Emails = nil, "", nil },
			want:   []string{"display_name", "owner", "emails"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := validAccount()
			tt.mutate(a)
			before := proto.Clone(a)

			err := fieldbehavior.ValidateRequired(a)
			assert.True(t, proto.Equal(before, a), "ValidateRequired never mutates")

			if tt.want == nil {
				require.NoError(t, err)
				return
			}

			require.Equal(t, tt.want, violationPaths(t, err))
		})
	}
}

// TestValidateRequiredSkipsServerManagedAndOptions pins that OUTPUT_ONLY and
// IDENTIFIER fields are not client input, that a nil message is valid, that
// WithMaxDepth bounds the walk and that WithResolver supplies the behaviors.
func TestValidateRequiredSkipsServerManagedAndOptions(t *testing.T) {
	t.Parallel()

	require.NoError(t, fieldbehavior.ValidateRequired(nil))

	resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		switch fd.Name() {
		case "update_time":
			return []annotations.FieldBehavior{annotations.FieldBehavior_REQUIRED, annotations.FieldBehavior_OUTPUT_ONLY}
		case "name":
			return []annotations.FieldBehavior{annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_REQUIRED}
		case "etag":
			return []annotations.FieldBehavior{annotations.FieldBehavior_REQUIRED}
		default:
			return nil
		}
	})
	err := fieldbehavior.ValidateRequired(&testpb.Account{}, fieldbehavior.WithResolver(resolver))
	require.Equal(t, []string{"etag"}, violationPaths(t, err), "OUTPUT_ONLY and IDENTIFIER are skipped, the etag has no behavior of its own")

	deep := validAccount()
	deep.Parent = validAccount()
	deep.Parent.Parent = validAccount()
	require.ErrorIs(t, fieldbehavior.ValidateRequired(deep, fieldbehavior.WithMaxDepth(1)), fieldbehavior.ErrMaxDepthExceeded)
	require.NoError(t, fieldbehavior.ValidateRequired(deep))

	require.ErrorIs(t, fieldbehavior.ValidateRequired(&testpb.Account{}, fieldbehavior.WithResolver(nil)), fieldbehavior.ErrBehaviorViolation,
		"a nil resolver reads the annotations")
}

// TestStrip_OutputOnlyNeverReported pins that OUTPUT_ONLY values are cleared
// at every depth — singular, repeated and map — and never reported, strict or
// not (AIP-203: the server must clear them and must not return an error).
func TestStrip_OutputOnlyNeverReported(t *testing.T) {
	t.Parallel()

	for _, strict := range []bool{false, true} {
		r := &testpb.Resource{
			CreateTime: "c",
			Profile:    &testpb.Profile{UpdatedAt: "u", DisplayName: "d"},
			Aliases:    []*testpb.Profile{{UpdatedAt: "u"}},
			Labels:     map[string]*testpb.Profile{"k": {UpdatedAt: "u"}},
			Tags:       []string{"t"},
			Audit:      &testpb.Profile{DisplayName: "a"},
		}

		err := fieldbehavior.Strip(r, fieldbehavior.WithBehaviors(annotations.FieldBehavior_OUTPUT_ONLY), fieldbehavior.WithStrict(strict))
		require.NoError(t, err, "strict=%v", strict)
		assert.Empty(t, r.GetCreateTime())
		assert.Empty(t, r.GetProfile().GetUpdatedAt())
		assert.Equal(t, "d", r.GetProfile().GetDisplayName())
		assert.Empty(t, r.GetAliases()[0].GetUpdatedAt())
		assert.Empty(t, r.GetLabels()["k"].GetUpdatedAt())
		assert.Empty(t, r.GetTags())
		assert.Nil(t, r.GetAudit())
	}
}

// TestStripUpdate_Immutable pins the AIP-203 IMMUTABLE rule on an update
// payload: an unchanged value is retained for later planning, a changed one is a
// violation, and without the stored resource protected values are kept; a rejected payload is left untouched.
func TestStripUpdate_Immutable(t *testing.T) {
	t.Parallel()

	stored := validAccount()

	tests := []struct {
		name    string
		mutate  func(*testpb.Account)
		current *testpb.Account
		want    []string
		reason  string
	}{
		{name: "unchanged", mutate: func(*testpb.Account) {}, current: stored},
		{name: "changed", mutate: func(a *testpb.Account) { a.Region = "us" }, current: stored, want: []string{"region"},
			reason: "immutable field cannot be changed from its stored value"},
		{name: "changed in a sub-message", mutate: func(a *testpb.Account) { a.Owner.Org = "org-2" }, current: stored,
			want: []string{"owner.org"}},
		{name: "set where the stored parent is absent", mutate: func(a *testpb.Account) { a.BackupOwner = &testpb.Owner{Org: "o"} },
			current: stored, want: []string{"backup_owner.org"}},
		{name: "without the stored resource", mutate: func(*testpb.Account) {}},
		{name: "changed identifier", mutate: func(a *testpb.Account) { a.Name = "accounts/2" }, current: stored, want: []string{"name"},
			reason: "identifier field cannot be changed from its stored value"},
		{
			name: "changed collection elements",
			mutate: func(a *testpb.Account) {
				a.Members = []*testpb.Owner{{Email: "m", Org: "new"}}
				a.Delegates = map[string]*testpb.Owner{"k": {Email: "d", Org: "new"}}
			},
			current: stored, want: []string{"members.0.org", "delegates.k.org"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := validAccount()
			tt.mutate(a)
			before := proto.Clone(a)

			var opts []fieldbehavior.Option
			if tt.current != nil {
				opts = append(opts, fieldbehavior.WithCurrent(tt.current))
			}

			err := fieldbehavior.StripUpdate(a, opts...)
			if tt.want != nil {
				require.Equal(t, tt.want, violationPaths(t, err))
				require.True(t, proto.Equal(before, a), "a rejected payload is left untouched")

				if tt.reason != "" {
					vErr, _ := errors.AsType[*fieldbehavior.BehaviorViolationError](err)
					assert.Equal(t, tt.reason, vErr.Violations[0].Reason)
					assert.Contains(t, err.Error(), tt.reason)
				}

				return
			}

			require.NoError(t, err)

			if tt.current == nil {
				assert.Equal(t, "eu", a.GetRegion())
				assert.Equal(t, "org-1", a.GetOwner().GetOrg())
			} else {
				assert.Equal(t, "eu", a.GetRegion(), "validated input survives later planning")
				assert.Equal(t, "org-1", a.GetOwner().GetOrg())
			}

			assert.Equal(t, "accounts/1", a.GetName(), "the IDENTIFIER is kept to address the resource")
			assert.Empty(t, a.GetUpdateTime(), "OUTPUT_ONLY is cleared")

			for _, m := range a.GetMembers() {
				assert.Equal(t, "new", m.GetOrg(), "an IMMUTABLE value of a new element is kept")
			}
		})
	}
}

// TestStripUpdate_StrictAndCurrentMismatch pins that strict mode does not
// report an unchanged IMMUTABLE value nor an OUTPUT_ONLY one, and that a
// stored resource of another type is refused.
func TestStripUpdate_StrictAndCurrentMismatch(t *testing.T) {
	t.Parallel()

	a := validAccount()
	require.NoError(t, fieldbehavior.StripUpdate(a, fieldbehavior.WithCurrent(validAccount()), fieldbehavior.WithStrict(true)))
	assert.Equal(t, "eu", a.GetRegion())
	assert.Empty(t, a.GetUpdateTime())

	require.ErrorIs(t, fieldbehavior.StripUpdate(validAccount(), fieldbehavior.WithCurrent(&testpb.Owner{})), fieldbehavior.ErrCurrentMismatch)
	require.NoError(t, fieldbehavior.StripUpdate(validAccount(), fieldbehavior.WithCurrent((*testpb.Account)(nil))),
		"a typed-nil stored resource is unknown")
	require.NoError(t, fieldbehavior.StripUpdate(&testpb.Owner{Note: "n"}, fieldbehavior.WithCurrent(&testpb.Owner{})))
}

// TestStripCreate_IdentifierClearedNotReported pins that the server assigns
// the identifier: StripCreate clears it silently by default (AIP-203: not
// accepted as input on create) and IMMUTABLE values are create input.
func TestStripCreate_IdentifierClearedNotReported(t *testing.T) {
	t.Parallel()

	a := validAccount()
	require.NoError(t, fieldbehavior.StripCreate(a))
	assert.Empty(t, a.GetName())
	assert.Empty(t, a.GetUpdateTime())
	assert.Equal(t, "eu", a.GetRegion())
	require.NoError(t, fieldbehavior.ValidateRequired(a))
}
