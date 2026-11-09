// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package apierror_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/protobuf/encoding/prototext"

	"github.com/altessa-s/go-aip/apierror"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

const secret = "secret stored value"

var errCorrupt = errors.New("stored record is invalid")

// assertNoSecret checks no text of the mapped error, its field violations or its google.rpc.Status names the hidden cause.
func assertNoSecret(t testing.TB, e *apierror.Error) {
	t.Helper()

	texts := []string{e.Message(), e.Error(), prototext.Format(e.Proto())}
	for _, v := range e.FieldViolations() {
		texts = append(texts, v.GetField(), v.GetDescription())
	}

	for _, text := range texts {
		assert.NotContains(t, strings.ToLower(text), secret)
		assert.NotContains(t, text, "storedField")
	}
}

// TestClassifyIgnoresCauses checks a classified error maps by its category alone, whatever its cause would map to: the cause is
// out of the errors.Is and errors.As chain and reachable through HiddenCause only, through the mapped error as well.
func TestClassifyIgnoresCauses(t *testing.T) {
	t.Parallel()

	clientErr := apierror.MustNew(code.Code_INVALID_ARGUMENT, "bad token", apierror.WithReason("BAD_TOKEN"))

	for name, tc := range map[string]struct {
		cause  error
		target error
	}{
		"deadline":         {cause: fmt.Errorf("decode: %w", context.DeadlineExceeded), target: context.DeadlineExceeded},
		"canceled":         {cause: context.Canceled, target: context.Canceled},
		"invalid argument": {cause: fmt.Errorf("%w: secret stored value", coreerrs.ErrInvalidArgument), target: coreerrs.ErrInvalidArgument},
		"field error":      {cause: coreerrs.NewFieldError("pageToken", "secret stored value"), target: coreerrs.ErrInvalidArgument},
		"api error":        {cause: fmt.Errorf("decode: %w", clientErr), target: clientErr},
		"not found":        {cause: coreerrs.ErrNotFound, target: coreerrs.ErrNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := fmt.Errorf("list widgets: %w", apierror.Classify(errCorrupt, tc.cause))
			require.ErrorIs(t, err, errCorrupt)
			require.NotErrorIs(t, err, tc.target, "the cause is out of the chain")
			assert.Equal(t, "list widgets: stored record is invalid", err.Error())
			assert.Same(t, tc.cause, apierror.HiddenCause(err))

			e := apierror.FromError(err)
			assert.Equal(t, code.Code_INTERNAL, e.Code())
			assert.Equal(t, apierror.ReasonInternal, e.Reason())
			assert.Equal(t, apierror.MessageInternal, e.Message())
			assert.Empty(t, e.FieldViolations())
			assert.Same(t, tc.cause, apierror.HiddenCause(e), "the cause stays reachable from the mapped error")
		})
	}

	_, ok := errors.AsType[*coreerrs.FieldError](apierror.Classify(errCorrupt, coreerrs.NewFieldError("f", "r")))
	assert.False(t, ok)
}

// TestClassifyHonorsTheCategory checks the category is classified as any error: by a rule of its own, by the default rules, or
// as an *Error.
func TestClassifyHonorsTheCategory(t *testing.T) {
	t.Parallel()

	m, err := apierror.NewMapper(apierror.WithRules(apierror.Rule{Target: errCorrupt, Code: code.Code_DATA_LOSS, Reason: "CORRUPT"}))
	require.NoError(t, err)

	e := m.Map(apierror.Classify(errCorrupt, context.DeadlineExceeded))
	assert.Equal(t, code.Code_DATA_LOSS, e.Code())
	assert.Equal(t, "CORRUPT", e.Reason())

	e = m.Map(apierror.Classify(fmt.Errorf("%w: gone", coreerrs.ErrNotFound), context.DeadlineExceeded))
	assert.Equal(t, code.Code_NOT_FOUND, e.Code())

	category := apierror.MustNew(code.Code_FAILED_PRECONDITION, "not ready", apierror.WithReason("NOT_READY"))
	e = m.Map(apierror.Classify(category, apierror.MustNew(code.Code_INVALID_ARGUMENT, "bad")))
	assert.Equal(t, code.Code_FAILED_PRECONDITION, e.Code())
	assert.Equal(t, "NOT_READY", e.Reason())
}

// TestClassifyOutsideTheChain checks only the classified branch of an error is cut: a sibling joined with it still classifies.
func TestClassifyOutsideTheChain(t *testing.T) {
	t.Parallel()

	err := errors.Join(apierror.Classify(errCorrupt, context.DeadlineExceeded), coreerrs.ErrNotFound)
	assert.Equal(t, code.Code_NOT_FOUND, apierror.FromError(err).Code())
}

// errHidden is a sentinel only a classified cause wraps.
var errHidden = errors.New("hidden decoder failure")

// TestClassifyHidesCausesFromTheVisibleSibling checks a classified branch joined with a visible client error stays out of the
// mapped status: its text is not in the exposed message and its FieldError gives no field violation, while the mapped error keeps
// the original error as its cause.
func TestClassifyHidesCausesFromTheVisibleSibling(t *testing.T) {
	t.Parallel()

	hidden := fmt.Errorf("%w: secret stored value: %w", coreerrs.ErrInvalidArgument, errHidden)
	visible := fmt.Errorf("%w: page size is negative", coreerrs.ErrInvalidArgument)

	for name, cause := range map[string]error{
		"invalid argument": hidden,
		"field error":      &coreerrs.FieldError{Field: "storedField", Reason: "secret stored value", Err: errHidden},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := fmt.Errorf("list widgets: %w", errors.Join(apierror.Classify(errCorrupt, cause), visible))

			e := apierror.FromError(err)
			assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
			assert.Equal(t, "list widgets: stored record is invalid\ninvalid argument: page size is negative", e.Message())
			assert.Empty(t, e.FieldViolations())
			assertNoSecret(t, e)
			assert.ErrorIs(t, apierror.HiddenCause(e), errHidden, "the mapped error keeps the original cause")
			assert.Same(t, err, errors.Unwrap(e))
		})
	}
}

// TestClassifyHidesCausesFromConvert checks a converter never reaches the classified causes, and the mapped error it builds keeps
// the original error as its cause.
func TestClassifyHidesCausesFromConvert(t *testing.T) {
	t.Parallel()

	errVisible := errors.New("visible")

	var seen error

	m, err := apierror.NewMapper(apierror.WithRules(apierror.Rule{
		Target: errVisible, Code: code.Code_FAILED_PRECONDITION,
		Convert: func(err error) *apierror.Error {
			seen = err

			return apierror.MustNew(code.Code_FAILED_PRECONDITION, err.Error(), apierror.WithCause(err))
		},
	}))
	require.NoError(t, err)

	in := errors.Join(apierror.Classify(errCorrupt, fmt.Errorf("secret: %w", errHidden)), errVisible)
	e := m.Map(in)

	require.Error(t, seen)
	assert.NotErrorIs(t, seen, errHidden)
	assert.ErrorIs(t, seen, errCorrupt)
	assert.ErrorIs(t, seen, errVisible)
	assert.NotContains(t, seen.Error(), "secret")
	assert.Equal(t, "stored record is invalid\nvisible", e.Message())
	assert.ErrorIs(t, apierror.HiddenCause(e), errHidden)
	assert.Same(t, in, errors.Unwrap(e))
}

// TestClassifyKeepsWrapperIdentity checks a wrapper above a classified error keeps its own errors.Is and errors.As matches and its
// own text, which can hold the category alone.
func TestClassifyKeepsWrapperIdentity(t *testing.T) {
	t.Parallel()

	joined := errors.Join(coreerrs.NewFieldError("title", "too long"), apierror.Classify(errCorrupt, errHidden))
	e := apierror.FromError(coreerrs.MapFieldPath(joined, func(f string) string { return "book." + f }))
	require.Len(t, e.FieldViolations(), 1)
	assert.Equal(t, "book.title", e.FieldViolations()[0].GetField())

	m, err := apierror.NewMapper(apierror.WithExposeUnknown(true))
	require.NoError(t, err)

	in := opaque{cause: apierror.Classify(errCorrupt, fmt.Errorf("secret: %w", errHidden))}
	e = m.Map(in)
	assert.Equal(t, code.Code_INTERNAL, e.Code())
	assert.Equal(t, "opaque failure: STORED RECORD IS INVALID", e.Message())

	m, err = apierror.NewMapper(apierror.WithRules(apierror.Rule{Target: errOpaque, Code: code.Code_FAILED_PRECONDITION, Expose: true}))
	require.NoError(t, err)

	e = m.Map(fmt.Errorf("wrapped: %w", in))
	assert.Equal(t, code.Code_FAILED_PRECONDITION, e.Code(), "the wrapper's own Is still matches")
	assert.Equal(t, "wrapped: opaque failure: STORED RECORD IS INVALID", e.Message())
}

// TestClassifyRedactsEveryOccurrence checks a wrapper that formats a classified error into its own text — more than once,
// reformatted, with %q, %+v or %#v, around a prefix and a suffix, or in nested wrappers — exposes no part of the hidden cause: a
// classified error renders as its category with every verb.
func TestClassifyRedactsEveryOccurrence(t *testing.T) {
	t.Parallel()

	c := apierror.Classify(errCorrupt, errors.New(secret))

	for name, w := range map[string]error{
		"repeated":       fmt.Errorf("%v; %w", c, c),
		"quoted":         fmt.Errorf("%q: %w", c, c),
		"plus v":         fmt.Errorf("%+v | %w", c, c),
		"go syntax":      fmt.Errorf("%#v %w", c, c),
		"prefix suffix":  fmt.Errorf("before %w after %v", c, c),
		"upper case":     fmt.Errorf("%s: %w", strings.ToUpper(c.Error()), c),
		"nested":         fmt.Errorf("outer %v: %w", c, fmt.Errorf("inner %v: %w", c, c)),
		"joined below":   fmt.Errorf("%v: %w", c, errors.Join(c, errors.New("other"))),
		"two wrapped":    fmt.Errorf("%w and %w (%v)", c, errors.New("other"), c),
		"no own wrapper": c,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := apierror.FromError(errors.Join(w, coreerrs.ErrInvalidArgument))
			require.Equal(t, code.Code_INVALID_ARGUMENT, e.Code(), "the message is exposed")
			assertNoSecret(t, e)
			assert.Contains(t, strings.ToLower(e.Message()), errCorrupt.Error())
			assert.Contains(t, e.Message(), coreerrs.ErrInvalidArgument.Error(), "the sibling keeps its text")
			assert.ErrorIs(t, e, c, "the mapped error keeps the original cause")
		})
	}
}

// namedStringError is a value-type error whose value is its text: %#v of a field holding it reflects that text.
type namedStringError string

func (e namedStringError) Error() string { return string(e) }

// structError is a value-type error with a string field: %#v of a field holding it reflects that field.
type structError struct{ Stored string }

func (structError) Error() string { return "struct error" }

// formatVerbs are the verbs and flags a classified error must render as its category alone.
var formatVerbs = []string{
	"%v", "%+v", "%#v", "%s", "%q", "%#q", "%+q", "%x", "%X", "% x", "%# X", "%d", "%T", "%10.4s", "%-40v", "%.3q", "%t", "%w",
}

// classifiedCauses are causes holding the secret in their text or in their value.
func classifiedCauses() map[string]error {
	return map[string]error{
		"text":         errors.New(secret),
		"named string": namedStringError(secret),
		"struct":       structError{Stored: secret},
		"wrapped":      fmt.Errorf("decode: %w", namedStringError(secret)),
		"pointer":      &structError{Stored: secret},
	}
}

// TestClassifyFormatsTheCategoryAlone checks formatting a classified error directly renders its category alone with every verb
// and flag, whatever value its cause holds, and keeps the verb's semantics for the category text.
func TestClassifyFormatsTheCategoryAlone(t *testing.T) {
	t.Parallel()

	for name, cause := range classifiedCauses() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := apierror.Classify(errCorrupt, cause)
			for _, verb := range formatVerbs {
				out := fmt.Sprintf(verb, c)
				assert.NotContains(t, out, secret, verb)
				assert.NotContains(t, out, "Stored", verb)
			}

			assert.Equal(t, errCorrupt.Error(), fmt.Sprintf("%v", c))
			assert.Equal(t, errCorrupt.Error(), fmt.Sprintf("%+v", c))
			assert.Equal(t, `"stored record is invalid"`, fmt.Sprintf("%#v", c))
			assert.Equal(t, `"stored record is invalid"`, fmt.Sprintf("%q", c))
			assert.Equal(t, "`stored record is invalid`", fmt.Sprintf("%#q", c))
			assert.Equal(t, fmt.Sprintf("%x", errCorrupt.Error()), fmt.Sprintf("%x", c))
			assert.Equal(t, fmt.Sprintf("% X", errCorrupt.Error()), fmt.Sprintf("% X", c))
			assert.Equal(t, "      stor", fmt.Sprintf("%10.4s", c))
			assert.Equal(t, `"stored record is invalid"`, c.(fmt.GoStringer).GoString())
			assert.Equal(t, fmt.Sprintf("%v", []error{errCorrupt}), fmt.Sprintf("%v", []error{c}))
			assert.NotContains(t, fmt.Sprintf("%#v", []error{c}), secret)
			assert.NotContains(t, fmt.Sprintf("%+v", struct{ Err error }{c}), secret)
			assert.NotContains(t, fmt.Sprintf("%#v", struct{ Err error }{c}), secret)
		})
	}
}

// TestClassifyFormattedIntoAnExposedWrapper checks a wrapper that formats a classified error with any verb, joined with a client
// error whose text the Mapper exposes, exposes no part of the hidden cause whatever value the cause holds.
func TestClassifyFormattedIntoAnExposedWrapper(t *testing.T) {
	t.Parallel()

	m, err := apierror.NewMapper()
	require.NoError(t, err)

	for name, cause := range classifiedCauses() {
		c := apierror.Classify(errCorrupt, cause)

		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			t.Run(name+"/"+verb, func(t *testing.T) {
				t.Parallel()

				w := fmt.Errorf("list: "+verb+": %w", c, c)
				for _, in := range []error{errors.Join(w, coreerrs.ErrInvalidArgument), fmt.Errorf("%w: %w", coreerrs.ErrInvalidArgument, w)} {
					e := m.Map(in)
					require.Equal(t, code.Code_INVALID_ARGUMENT, e.Code(), "the message is exposed")
					assertNoSecret(t, e)
					assert.NotContains(t, prototext.Format(e.Proto()), "Stored")
					assert.Equal(t, cause, apierror.HiddenCause(e), "the mapped error keeps the hidden cause")
				}
			})
		}
	}
}

var errOpaque = errors.New("opaque")

// opaque renders its own text, which does not hold its cause's.
type opaque struct{ cause error }

func (o opaque) Error() string { return "opaque failure: " + strings.ToUpper(o.cause.Error()) }
func (o opaque) Unwrap() error { return o.cause }

func (opaque) Is(target error) bool { return target == errOpaque }

func TestClassifyNil(t *testing.T) {
	t.Parallel()

	cause := errors.New("cause")

	assert.Equal(t, errCorrupt, apierror.Classify(errCorrupt, nil))
	assert.Equal(t, cause, apierror.Classify(nil, cause))
	assert.NoError(t, apierror.Classify(nil, nil))
	assert.Equal(t, "stored record is invalid", apierror.Classify(errCorrupt, cause).Error())
	assert.NoError(t, apierror.HiddenCause(nil))
	assert.NoError(t, apierror.HiddenCause(cause))
}

// TestClassifyLogValue checks a classified error renders its hidden cause for a server log.
func TestClassifyLogValue(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	slog.New(slog.NewTextHandler(&buf, nil)).Error("list", "err", apierror.Classify(errCorrupt, errors.New("bad stored value")))
	assert.Contains(t, buf.String(), `err="stored record is invalid: bad stored value"`)
}

// selfClassified is a Classified error of its own type: it unwraps to nothing.
type selfClassified struct{ cause error }

func (selfClassified) Error() string        { return "self classified" }
func (s selfClassified) HiddenCause() error { return s.cause }

func TestCustomClassified(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("wrap: %w", selfClassified{cause: coreerrs.ErrNotFound})

	e := apierror.FromError(err)
	assert.Equal(t, code.Code_INTERNAL, e.Code())
	assert.Equal(t, apierror.MessageInternal, e.Message())
	assert.Equal(t, coreerrs.ErrNotFound, apierror.HiddenCause(e))
}

// TestClassifyHidesCausesFromReRooting checks coreerrs.MapFieldPath never re-roots a FieldError a classified error hides, whatever
// its reason, an empty one included, mapped once or twice: no message, field violation or status detail names the hidden field
// or reason, while the original error stays the cause.
func TestClassifyHidesCausesFromReRooting(t *testing.T) {
	t.Parallel()

	prefix := func(f string) string { return "book." + f }
	visible := fmt.Errorf("%w: page size is negative", coreerrs.ErrInvalidArgument)

	for _, reason := range []string{secret, ""} {
		hidden := &coreerrs.FieldError{Field: "storedField", Reason: reason, Err: errHidden}
		c := apierror.Classify(errCorrupt, fmt.Errorf("decode: %w", hidden))

		for name, tc := range map[string]struct {
			err     error
			code    code.Code
			message string
		}{
			"mapped path":  {err: coreerrs.MapFieldPath(c, prefix), code: code.Code_INTERNAL},
			"mapped twice": {err: coreerrs.MapFieldPath(coreerrs.MapFieldPath(c, prefix), prefix), code: code.Code_INTERNAL},
			"mapped beside a visible error": {
				err:     coreerrs.MapFieldPath(errors.Join(c, visible), prefix),
				code:    code.Code_INVALID_ARGUMENT,
				message: "stored record is invalid\ninvalid argument: page size is negative",
			},
		} {
			t.Run(name+"/reason="+reason, func(t *testing.T) {
				t.Parallel()

				e := apierror.FromError(tc.err)
				assert.Equal(t, tc.code, e.Code())
				assert.Empty(t, e.FieldViolations())
				assertNoSecret(t, e)

				if tc.message != "" {
					assert.Equal(t, tc.message, e.Message())
				}

				assert.ErrorIs(t, apierror.HiddenCause(e), hidden, "the mapped error keeps the original cause")
			})
		}
	}
}

// TestClassifyKeepsVisibleFieldErrors checks a visible FieldError beside a classified one keeps its violation and its re-rooted
// path, whichever comes first and whatever reason the hidden FieldError has, the same reason included.
func TestClassifyKeepsVisibleFieldErrors(t *testing.T) {
	t.Parallel()

	prefix := func(f string) string { return "book." + f }

	for _, reason := range []string{"required", "", "too long"} {
		c := apierror.Classify(errCorrupt, coreerrs.NewFieldError("storedField", reason))
		title := coreerrs.NewFieldError("title", "required")

		for name, err := range map[string]error{
			"visible first":    coreerrs.MapFieldPath(errors.Join(title, c), prefix),
			"classified first": coreerrs.MapFieldPath(errors.Join(c, title), prefix),
			"mapped twice":     coreerrs.MapFieldPath(coreerrs.MapFieldPath(errors.Join(c, title), prefix), func(f string) string { return "x." + f }),
			"unrelated wrapper": derived{
				extra: coreerrs.MapFieldPath(title, prefix), err: c,
			},
		} {
			t.Run(name+"/reason="+reason, func(t *testing.T) {
				t.Parallel()

				e := apierror.FromError(err)
				assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
				require.Len(t, e.FieldViolations(), 1)
				assert.Contains(t, []string{"book.title", "x.book.title"}, e.FieldViolations()[0].GetField())
				assert.Equal(t, "required", e.FieldViolations()[0].GetDescription())
				assertNoSecret(t, e)
			})
		}
	}
}

// derived wraps an error together with another one, the way coreerrs.MapFieldPath does.
type derived struct {
	extra error
	err   error
}

func (d derived) Error() string   { return d.err.Error() }
func (d derived) Unwrap() []error { return []error{d.extra, d.err} }

// TestClassifyFieldErrorAboveIt checks a FieldError above a classified error is a client error of its own: its reason can format
// the classified error, which renders as the category alone, and a FieldError the classification holds is reported.
func TestClassifyFieldErrorAboveIt(t *testing.T) {
	t.Parallel()

	c := apierror.Classify(errCorrupt, coreerrs.NewFieldError("storedField", secret))

	e := apierror.FromError(&coreerrs.FieldError{Field: "item", Reason: c.Error(), Err: c})
	assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
	require.Len(t, e.FieldViolations(), 1)
	assert.Equal(t, "item", e.FieldViolations()[0].GetField())
	assert.Equal(t, errCorrupt.Error(), e.FieldViolations()[0].GetDescription())
	assertNoSecret(t, e)

	e = apierror.FromError(fmt.Errorf("list: %w", apierror.Classify(coreerrs.NewFieldError("pageToken", "malformed"), errors.New(secret))))
	assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
	require.Len(t, e.FieldViolations(), 1)
	assert.Equal(t, "pageToken", e.FieldViolations()[0].GetField())
	assertNoSecret(t, e)
}

// TestClassifyAPIErrorCategoryKeepsTheCause: review finding R9-F2 — a classified error whose category is an *Error mapped to that
// *Error as it was, so the classification and the cause it hides were dropped and HiddenCause of the mapped error was nil. The
// mapped error is the category, with the domain filled in, and keeps the original error as its cause, directly or wrapped; the
// cause stays hidden.
func TestClassifyAPIErrorCategoryKeepsTheCause(t *testing.T) {
	t.Parallel()

	category := apierror.MustNew(code.Code_DATA_LOSS, "stored record is corrupt", apierror.WithReason("CORRUPT"))
	hidden := fmt.Errorf("%w: secret stored value: %w", coreerrs.ErrInvalidArgument, errHidden)

	m, err := apierror.NewMapper(apierror.WithDefaultDomain("example.com"))
	require.NoError(t, err)

	for name, in := range map[string]error{
		"direct":  apierror.Classify(category, hidden),
		"wrapped": fmt.Errorf("list widgets: %w", apierror.Classify(category, hidden)),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := m.Map(in)
			assert.Equal(t, code.Code_DATA_LOSS, e.Code())
			assert.Equal(t, "CORRUPT", e.Reason())
			assert.Equal(t, "stored record is corrupt", e.Message())
			assert.Equal(t, "example.com", e.Domain())
			assert.Empty(t, category.Domain(), "the category itself is not changed")
			assert.Same(t, hidden, apierror.HiddenCause(e))
			assert.Same(t, in, errors.Unwrap(e))
			require.ErrorIs(t, e, category)
			assert.NotErrorIs(t, e, errHidden)
			assert.NotErrorIs(t, e, coreerrs.ErrInvalidArgument)
			assertNoSecret(t, e)
		})
	}
}

// TestConvertKeepsTheMatchedError: review finding R9-F2 — the error a converter built was returned as it was, so a converter
// that kept no cause dropped the original error, and the cause a classified error in it hides, from the mapped error. The mapped
// error keeps the matched error as its cause, joined with a cause of the converter's own; a converter that kept it is unchanged.
func TestConvertKeepsTheMatchedError(t *testing.T) {
	t.Parallel()

	errVisible := errors.New("visible")
	errOwn := errors.New("converter cause")

	for name, tc := range map[string]struct {
		convert func(error) *apierror.Error
		own     bool
	}{
		"without a cause": {convert: func(error) *apierror.Error {
			return apierror.MustNew(code.Code_FAILED_PRECONDITION, "not ready")
		}},
		"with a cause of its own": {convert: func(error) *apierror.Error {
			return apierror.MustNew(code.Code_FAILED_PRECONDITION, "not ready", apierror.WithCause(errOwn))
		}, own: true},
		"with the matched error": {convert: func(err error) *apierror.Error {
			return apierror.MustNew(code.Code_FAILED_PRECONDITION, "not ready", apierror.WithCause(err))
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m, err := apierror.NewMapper(apierror.WithRules(apierror.Rule{
				Target: errVisible, Code: code.Code_FAILED_PRECONDITION, Convert: tc.convert,
			}))
			require.NoError(t, err)

			in := errors.Join(apierror.Classify(errCorrupt, fmt.Errorf("secret stored value: %w", errHidden)), errVisible)
			e := m.Map(in)
			assert.Equal(t, code.Code_FAILED_PRECONDITION, e.Code())
			assert.Equal(t, "not ready", e.Message())
			assert.ErrorIs(t, apierror.HiddenCause(e), errHidden)
			assert.ErrorIs(t, e, errVisible)
			assert.Equal(t, tc.own, errors.Is(e, errOwn))
			assert.NotErrorIs(t, e, errHidden)
			assertNoSecret(t, e)
		})
	}
}
