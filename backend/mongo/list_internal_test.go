// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/protobuf/encoding/prototext"

	"github.com/altessa-s/go-atlas-core/time/civil"
	"github.com/altessa-s/go-atlas-core/types/optional"

	"github.com/altessa-s/go-aip/apierror"
	"github.com/altessa-s/go-aip/apierror/aipmap"
	"github.com/altessa-s/go-aip/backend/mongo/internal/keyset"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
	"github.com/altessa-s/go-aip/pagination/pagetoken"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestIndependentNumericScope(t *testing.T) {
	t.Parallel()

	list, err := NewListEngine[listingtest.Widget](nil, nil, listingtest.NewSchema(t), WithListScopeKeys("tenant"))
	require.NoError(t, err)
	require.NoError(t, list.checkScope(t.Context(), bson.D{{Key: "tenant", Value: int64(42)}}, ""))
	require.ErrorIs(t, list.checkScope(t.Context(), nil, ""), ErrScope)
}

func TestParentBindingRequiresDeclaredKey(t *testing.T) {
	t.Parallel()

	_, err := NewListEngine[listingtest.Widget](nil, nil, listingtest.NewSchema(t), WithListParentKey("owner"))
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
}

func TestParentKeyCanBeDisabled(t *testing.T) {
	t.Parallel()

	require.Empty(t, newListOptions(WithListParentKey("owner"), WithListParentKey("")).parentKey)
	require.Equal(t, "owner", newListOptions(WithListParentKey(" owner ")).parentKey)
}

func TestTokenErrorFormatter(t *testing.T) {
	t.Parallel()

	marker := errors.New("formatted")
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"},
		listing.NewProfile(listing.WithErrorFormatter(func(field, reason string, cause error) error {
			require.Equal(t, pagetoken.TokenField, field)
			require.NotEmpty(t, reason)

			return errors.Join(marker, cause)
		})))
	require.NoError(t, err)

	l := &ListEngine[map[string]any]{schema: schema, opts: newListOptions()}
	err = l.mapError(t.Context(), t.Context(), pagetoken.ErrInvalidToken, false)
	require.ErrorIs(t, err, marker)
	require.ErrorIs(t, err, pagetoken.ErrInvalidToken)
}

// TestListRegistryEncodesDecimals checks the default list registry encodes a decimal filter value as a Decimal128 (the driver's
// default registry writes an empty document), so the page token binds the bound and the server receives it.
func TestListRegistryEncodesDecimals(t *testing.T) {
	t.Parallel()

	price := func(v string) bson.D {
		return bson.D{{Key: "price", Value: bson.D{{Key: "$gte", Value: decimal.RequireFromString(v)}}}}
	}

	raw, err := marshal(price("10.5"), listRegistry())
	require.NoError(t, err)

	got := raw.Lookup("price", "$gte")
	require.Equal(t, bson.TypeDecimal128, got.Type)
	require.Equal(t, "10.5", got.Decimal128().String())

	other, err := marshal(price("99"), listRegistry())
	require.NoError(t, err)
	require.NotEqual(t, raw, other, "different decimal bounds are different filters")

	_, err = marshal(bson.D{{Key: "f", Value: func() {}}}, listRegistry())
	require.Error(t, err)
}

// TestScopePinsUseTheListRegistry checks a scope pin is judged by the encoding of the engine's registry: a decimal pins its key
// as a Decimal128.
func TestScopePinsUseTheListRegistry(t *testing.T) {
	t.Parallel()

	list, err := NewListEngine[listingtest.Widget](nil, nil, listingtest.NewSchema(t), WithListScopeKeys("tenant"))
	require.NoError(t, err)
	require.NoError(t, list.checkScope(t.Context(), bson.D{{Key: "tenant", Value: decimal.RequireFromString("1.5")}}, ""))
	require.ErrorIs(t, list.checkScope(t.Context(), bson.D{{Key: "tenant", Value: func() {}}}, ""), ErrScope)
}

// TestScopeAdmitsOnlyFilterTypes checks a scope holding a value of a type a filter does not admit — a struct, a pointer, an
// optional.Optional of a non-leaf, a value with its own BSON marshaling — fails with ErrScope wrapping keyset.ErrValueType before
// it is encoded, as a pin or as any other scope element, while admitted pins pass.
func TestScopeAdmitsOnlyFilterTypes(t *testing.T) {
	t.Parallel()

	list, err := NewListEngine[listingtest.Widget](nil, nil, listingtest.NewSchema(t), WithListScopeKeys("tenant"))
	require.NoError(t, err)

	type pin struct{ ID string }

	s := "t1"

	for name, v := range map[string]any{
		"struct":            pin{ID: "t1"},
		"pointer":           &s,
		"pointer to struct": &pin{},
		"optional struct":   optional.Some(pin{}),
		"optional pointer":  optional.Some(&s),
		"page type":         listingtest.Widget{},
	} {
		for where, scope := range map[string]bson.D{
			"pin":   {{Key: "tenant", Value: v}},
			"other": {{Key: "tenant", Value: "t1"}, {Key: "kind", Value: bson.D{{Key: "$in", Value: bson.A{v}}}}},
		} {
			err := list.checkScope(t.Context(), scope, "")
			require.ErrorIs(t, err, ErrScope, "%s as %s", name, where)
			require.ErrorIs(t, err, keyset.ErrValueType, "%s as %s", name, where)
		}
	}

	for name, v := range map[string]any{
		"string":          "t1",
		"optional string": optional.Some("t1"),
		"date":            civil.Date{Year: 2026, Month: time.November, Day: 9},
		"optional date":   optional.Some(civil.Date{Year: 2026, Month: time.November, Day: 9}),
		"decimal":         decimal.RequireFromString("1.5"),
		"object id":       bson.NewObjectID(),
		"named number":    time.Duration(1),
	} {
		require.NoError(t, list.checkScope(t.Context(), bson.D{{Key: "tenant", Value: v}}, ""), name)
	}
}

// scopeDate is a service's own calendar-date type, admitted as a scope value by the registry it is registered on.
type scopeDate struct {
	Year  int
	Month time.Month
	Day   int
}

// TestScopeAdmitsRegisteredDates checks a date type registered with RegisterDateType on the engine's registry pins a scope key,
// and stays refused by an engine whose registry does not know it.
func TestScopeAdmitsRegisteredDates(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	require.NoError(t, RegisterDateType[scopeDate](reg))

	date := scopeDate{Year: 2026, Month: time.November, Day: 9}

	list, err := NewListEngine[listingtest.Widget](nil, nil, listingtest.NewSchema(t), WithListScopeKeys("day"), WithListRegistry(reg))
	require.NoError(t, err)
	require.NoError(t, list.checkScope(t.Context(), bson.D{{Key: "day", Value: date}}, ""))

	other, err := NewListEngine[listingtest.Widget](nil, nil, listingtest.NewSchema(t), WithListScopeKeys("day"))
	require.NoError(t, err)
	require.ErrorIs(t, other.checkScope(t.Context(), bson.D{{Key: "day", Value: date}}, ""), keyset.ErrValueType)
}

// sharedGraph returns a small value whose encoding repeats its parts 2^levels times.
func sharedGraph(levels int) any {
	var v any = bson.A{int32(1)}
	for range levels {
		v = bson.A{v, v}
	}

	return v
}

// TestBoundedScopeFails checks a scope pin, or another scope element, that refers to itself or shares its parts so that its
// encoding would expand beyond the limits fails fast with ErrScope, before the driver encodes it, and that a canceled context
// stops the check with its own error.
func TestBoundedScopeFails(t *testing.T) {
	t.Parallel()

	list, err := NewListEngine[listingtest.Widget](nil, nil, listingtest.NewSchema(t), WithListScopeKeys("tenant"))
	require.NoError(t, err)

	cyclic := bson.A{nil}
	cyclic[0] = cyclic

	start := time.Now()

	require.ErrorIs(t, list.checkScope(t.Context(), bson.D{{Key: "tenant", Value: sharedGraph(40)}}, ""), ErrScope)
	require.ErrorIs(t, list.checkScope(t.Context(), bson.D{{Key: "tenant", Value: cyclic}}, ""), ErrScope)

	_, err = list.List(t.Context(), "widgets.List", listing.Request{PageSize: 10},
		bson.D{{Key: "tenant", Value: int64(1)}, {Key: "tags", Value: sharedGraph(40)}})
	require.ErrorIs(t, err, ErrScope)
	requireQuick(t, time.Since(start), raceSlowdown*time.Second)

	require.NoError(t, list.checkScope(t.Context(), bson.D{{Key: "tenant", Value: int64(1)}, {Key: "tags", Value: sharedGraph(8)}}, ""))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err = list.checkScope(ctx, bson.D{{Key: "tenant", Value: int64(1)}, {Key: "tags", Value: make([]bson.M, 1<<16)}}, "")
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, ErrScope)
}

// TestScopeRefusesDangerousOperators checks a scope holding a server-side JavaScript operator, raw or not, fails with ErrScope.
func TestScopeRefusesDangerousOperators(t *testing.T) {
	t.Parallel()

	list, err := NewListEngine[listingtest.Widget](nil, nil, listingtest.NewSchema(t))
	require.NoError(t, err)

	raw, err := bson.Marshal(bson.D{{Key: "$where", Value: "sleep(1000)"}})
	require.NoError(t, err)

	for name, scope := range map[string]bson.D{
		"document": {{Key: "$where", Value: "sleep(1000)"}},
		"raw":      {{Key: "$and", Value: bson.A{bson.Raw(raw)}}},
	} {
		err := list.checkScope(t.Context(), scope, "")
		require.ErrorIs(t, err, ErrScope, name)
		require.ErrorIs(t, err, keyset.ErrDangerousOperator, name)
	}
}

// TestPresenceForm pins the MongoDB filter of the Presence field rules of the listing contract: null differs from every value
// (MongoDB's own $ne, $nin and $nor), an ordering comparison is unknown on null also under negation (the complemented
// comparison), and has() holds for a non-null value only.
func TestPresenceForm(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "rating", Kind: filter.FieldKindInt, Presence: true},
		{Name: "score", Kind: filter.FieldKindString, Presence: true},
		{Name: "meta.level", Kind: filter.FieldKindInt, Presence: true},
	}, []string{"id"}, listing.NewProfile(listing.WithBuiltinFunctions("has", "startsWith")))
	require.NoError(t, err)

	l, err := NewListEngine[bson.M](nil, nil, schema, WithListFieldPaths(map[string]string{"id": "_id"}))
	require.NoError(t, err)

	for expr, want := range map[string]string{
		`rating != 1`:                     `{"rating":{"$ne":1}}`,
		`!(rating == 1)`:                  `{"rating":{"$ne":1}}`,
		`!(rating > 1)`:                   `{"rating":{"$lte":1}}`,
		`!(rating in [1, 2])`:             `{"$nor":[{"rating":{"$in":[1,2]}}]}`,
		`has(meta.level)`:                 `{"meta.level":{"$ne":null}}`,
		`!has(meta.level)`:                `{"meta.level":null}`,
		`!score.startsWith("a")`:          `{"$nor":[{"score":{"$regex":"^a"}}]}`,
		`!(has(meta.level) && id == "x")`: `{"$or":[{"meta.level":null},{"$nor":[{"_id":"x"}]}]}`,
	} {
		q, err := schema.Compile(t.Context(), listing.Request{Filter: expr})
		require.NoError(t, err, expr)

		d, err := l.translate(q.Filter)
		require.NoError(t, err, expr)

		got, err := bson.MarshalExtJSON(d, false, false)
		require.NoError(t, err, expr)
		require.JSONEq(t, want, string(got), expr)
	}
}

// TestPresenceFormKeepsTheOperationBudget checks the operation limit applies to the filter as the client wrote it: the
// presence rewrite adds nodes (has(meta.note) becomes meta.note != null, !flag becomes flag == false), and a filter Schema.Compile
// accepts within the limit is not refused by the engine for them. A filter beyond the limit and a field outside the
// allow-list are still refused.
func TestPresenceFormKeepsTheOperationBudget(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "meta.note", Kind: filter.FieldKindString, Presence: true},
		{Name: "flag", Kind: filter.FieldKindBool, Presence: true},
	}, []string{"id"}, listing.NewProfile(listing.WithBuiltinFunctions("has"), listing.WithMaxFilterOperations(10)))
	require.NoError(t, err)

	l, err := NewListEngine[bson.M](nil, nil, schema, WithListFieldPaths(map[string]string{"id": "_id"}))
	require.NoError(t, err)

	for _, expr := range []string{
		`has(meta.note) && has(meta.note) && has(meta.note)`, // 8 nodes, 11 once rewritten
		`!flag && !flag && !flag`,                            // 8 nodes, 11 once rewritten
	} {
		q, err := schema.Compile(t.Context(), listing.Request{Filter: expr})
		require.NoError(t, err, expr)

		_, err = l.translate(q.Filter)
		require.NoError(t, err, expr)
	}

	q, err := schema.Compile(t.Context(), listing.Request{Filter: `has(meta.note)`})
	require.NoError(t, err)

	over := &filter.BinaryOpNode{Op: filter.OpAnd, Left: q.Filter, Right: &filter.BinaryOpNode{Op: filter.OpAnd, Left: q.Filter,
		Right: &filter.BinaryOpNode{Op: filter.OpAnd, Left: q.Filter, Right: q.Filter}}} // 11 nodes
	_, err = l.translate(over)
	require.ErrorIs(t, err, filter.ErrMaxOperationsExceeded)

	denied := &filter.BinaryOpNode{Op: filter.OpAnd, Left: q.Filter, Right: &filter.CallNode{Op: filter.OpHas, Target: &filter.IdentNode{Name: "secret"}}}
	_, err = l.translate(denied)
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
}

// TestMapError pins the classification of query failures: the time cap on a filtered and an unfiltered query, the caller's own
// deadline, a decode failure in each of its forms, the error mapper, and any other failure wrapped with the collection name.
func TestMapError(t *testing.T) {
	t.Parallel()

	s := listingtest.NewSchema(t)
	l, err := NewListEngine[listingtest.Widget](nil, nil, s, WithListFieldPaths(listingtest.FieldPaths()))
	require.NoError(t, err)
	require.Same(t, s, l.Schema())

	err = l.mapError(t.Context(), t.Context(), context.DeadlineExceeded, true)
	require.Equal(t, listing.FieldFilter, listingtest.FieldOf(err))
	require.ErrorIs(t, err, ErrTimeout)

	err = l.mapError(t.Context(), t.Context(), context.DeadlineExceeded, false)
	require.ErrorIs(t, err, coreerrs.ErrUnavailable)
	require.ErrorIs(t, err, ErrTimeout)

	done, cancel := context.WithCancel(t.Context())
	cancel()
	require.NotErrorIs(t, l.mapError(done, done, context.DeadlineExceeded, false), ErrTimeout, "the caller's deadline is not the cap")

	for name, decodeErr := range map[string]error{
		"decode error":        &bson.DecodeError{},
		"value decoder error": bson.ValueDecoderError{Name: "x"},
		"page type error":     &keyset.DecodeError{Err: errors.New("unmarshal")},
	} {
		err = l.mapError(t.Context(), t.Context(), fmt.Errorf("read: %w", decodeErr), false)
		require.ErrorIs(t, err, ErrCorruptRecord, name)
		require.Contains(t, err.Error(), "<no collection>", name)
	}

	// A page type's decoder returning a timeout or token sentinel is still a record that does not decode.
	for name, cause := range map[string]error{
		"deadline": context.DeadlineExceeded,
		"token":    pagetoken.ErrInvalidToken,
	} {
		for _, filtered := range []bool{false, true} {
			err = l.mapError(t.Context(), t.Context(), &keyset.DecodeError{Err: cause}, filtered)
			require.ErrorIs(t, err, ErrCorruptRecord, name)
			require.NotErrorIs(t, err, cause, name)
			require.ErrorIs(t, apierror.HiddenCause(err), cause, name)
			require.NotErrorIs(t, err, ErrTimeout, name)
			require.NotErrorIs(t, err, coreerrs.ErrUnavailable, name)
			require.Empty(t, listingtest.FieldOf(err), name)
		}
	}

	// An API error mapper classifies the record by its category, not by the decoder's cause, and exposes none of its text.
	mapper, err := apierror.NewMapper(apierror.WithRules(aipmap.Rules()...))
	require.NoError(t, err)

	for name, cause := range map[string]error{
		"deadline":    fmt.Errorf("stored value: %w", context.DeadlineExceeded),
		"token":       fmt.Errorf("stored value %q: %w", "secret", pagetoken.ErrInvalidToken),
		"field error": coreerrs.NewFieldError("pageToken", "stored value secret"),
		"bson":        &bson.DecodeError{},
	} {
		for _, filtered := range []bool{false, true} {
			err = l.mapError(t.Context(), t.Context(), &keyset.DecodeError{Err: cause}, filtered)
			require.ErrorIs(t, apierror.HiddenCause(err), cause, name)

			e := mapper.Map(err)
			require.Equal(t, code.Code_INTERNAL, e.Code(), name)
			require.Equal(t, apierror.MessageInternal, e.Message(), name)
			require.Empty(t, e.FieldViolations(), name)
		}
	}

	category := errors.New("corrupt")
	custom, err := NewListEngine[listingtest.Widget](nil, nil, s, WithListCorruptRecord(category))
	require.NoError(t, err)

	err = custom.mapError(t.Context(), t.Context(), &keyset.DecodeError{Err: context.DeadlineExceeded}, true)
	require.ErrorIs(t, err, category)
	require.ErrorIs(t, apierror.HiddenCause(err), context.DeadlineExceeded)
	require.NotErrorIs(t, err, ErrTimeout)

	// An API error as the category maps to that error, keeping the decoder failure as its hidden cause (review finding R9-F2).
	apiCategory := apierror.MustNew(code.Code_DATA_LOSS, "stored record is corrupt", apierror.WithReason("CORRUPT"))
	apiCustom, err := NewListEngine[listingtest.Widget](nil, nil, s, WithListCorruptRecord(apiCategory))
	require.NoError(t, err)

	decoderFailure := fmt.Errorf("stored value %q: %w", "secret", pagetoken.ErrInvalidToken)
	e := mapper.Map(apiCustom.mapError(t.Context(), t.Context(), &keyset.DecodeError{Err: decoderFailure}, false))
	require.Equal(t, code.Code_DATA_LOSS, e.Code())
	require.Equal(t, "stored record is corrupt", e.Message())
	require.ErrorIs(t, apierror.HiddenCause(e), decoderFailure)
	require.NotErrorIs(t, e, pagetoken.ErrInvalidToken)
	require.NotContains(t, e.Error(), "secret")

	require.ErrorIs(t, classifyDecode(ErrCorruptRecord, ErrCorruptRecord), ErrCorruptRecord)

	classified := apierror.Classify(ErrCorruptRecord, &keyset.DecodeError{Err: context.DeadlineExceeded})
	require.Same(t, classified, classifyDecode(classified, ErrCorruptRecord), "an error classified by the category stays as it is")

	// A decoder failure whose chain already classifies one cause still classifies as a whole: neither a deadline nor an API error
	// joined with that cause selects the status.
	clientErr := apierror.MustNew(code.Code_INVALID_ARGUMENT, "secret stored value", apierror.WithReason("BAD_TOKEN"))
	for name, sibling := range map[string]error{
		"deadline":  context.DeadlineExceeded,
		"api error": clientErr,
		"canceled":  fmt.Errorf("stored value: %w", context.Canceled),
	} {
		decodeErr := errors.Join(apierror.Classify(ErrCorruptRecord, errors.New("bad stored value")), sibling)

		err = l.mapError(t.Context(), t.Context(), &keyset.DecodeError{Err: decodeErr}, false)
		require.NotErrorIs(t, err, sibling, name)
		require.ErrorIs(t, apierror.HiddenCause(err), sibling, name)

		e := mapper.Map(err)
		require.Equal(t, code.Code_INTERNAL, e.Code(), name)
		require.Equal(t, apierror.MessageInternal, e.Message(), name)
	}

	other := errors.New("network")
	err = l.mapError(t.Context(), t.Context(), other, false)
	require.ErrorIs(t, err, other)
	require.NotErrorIs(t, err, ErrCorruptRecord)

	marker := errors.New("mapped")
	mapped, err := NewListEngine[listingtest.Widget](nil, nil, s, WithListErrorMapper(func(_ context.Context, err error, filtered bool) error {
		require.True(t, filtered)

		return errors.Join(marker, err)
	}))
	require.NoError(t, err)
	require.ErrorIs(t, mapped.mapError(t.Context(), t.Context(), other, true), marker)
}

// TestMapErrorHidesCausesWhenTheQueryEnds checks a page read failing after the query context ended — the time cap or the caller's
// context — reports that end alone whatever FindPage returned (a decoder, cursor or token error), as does a driver timeout: no
// status, text or sentinel of the failure is visible, and apierror.HiddenCause keeps it for logs.
func TestMapErrorHidesCausesWhenTheQueryEnds(t *testing.T) {
	t.Parallel()

	l, err := NewListEngine[listingtest.Widget](nil, nil, listingtest.NewSchema(t), WithListFieldPaths(listingtest.FieldPaths()))
	require.NoError(t, err)

	mapper, err := apierror.NewMapper(apierror.WithRules(aipmap.Rules()...))
	require.NoError(t, err)

	expired, cancelExpired := context.WithDeadline(t.Context(), time.Unix(0, 0))
	t.Cleanup(cancelExpired)

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	noSecret := func(t *testing.T, e *apierror.Error) {
		t.Helper()

		texts := []string{e.Message(), e.Error(), e.Reason(), prototext.Format(e.Proto())}
		for _, v := range e.FieldViolations() {
			texts = append(texts, v.GetField(), v.GetDescription())
		}

		for _, text := range texts {
			require.NotContains(t, strings.ToLower(text), "secret")
			require.NotContains(t, text, "storedField")
		}
	}

	for name, cause := range map[string]error{
		"field error": coreerrs.NewFieldError("storedField", "secret stored value"),
		"api error":   apierror.MustNew(code.Code_INVALID_ARGUMENT, "secret stored value", apierror.WithReason("SECRET_REASON")),
		"token":       fmt.Errorf("stored value %q: %w", "secret", pagetoken.ErrInvalidToken),
		"deadline":    fmt.Errorf("secret stored value: %w", context.DeadlineExceeded),
	} {
		failures := map[string]error{
			"decode": &keyset.DecodeError{Err: cause},
			"cursor": fmt.Errorf("mongo: read page: %w", cause),
		}

		for failureName, failure := range failures {
			for _, filtered := range []bool{false, true} {
				label := fmt.Sprintf("%s/%s/filtered=%t", name, failureName, filtered)

				// The time cap expired while the caller's context is alive.
				err := l.mapError(t.Context(), expired, failure, filtered)
				require.ErrorIs(t, err, ErrTimeout, label)
				require.NotErrorIs(t, err, cause, label)
				require.NotErrorIs(t, err, ErrCorruptRecord, label)
				require.ErrorIs(t, apierror.HiddenCause(err), cause, label)
				require.Contains(t, apierror.HiddenCause(err).Error(), "secret", label)

				e := mapper.Map(err)
				noSecret(t, e)

				if filtered {
					require.Equal(t, code.Code_INVALID_ARGUMENT, e.Code(), label)
					require.Len(t, e.FieldViolations(), 1, label)
					require.Equal(t, listing.FieldFilter, e.FieldViolations()[0].GetField(), label)
				} else {
					require.Equal(t, code.Code_UNAVAILABLE, e.Code(), label)
					require.Empty(t, e.FieldViolations(), label)
				}

				// The caller's context ended.
				err = l.mapError(canceled, canceled, failure, filtered)
				require.ErrorIs(t, err, context.Canceled, label)
				require.NotErrorIs(t, err, cause, label)
				require.NotErrorIs(t, err, ErrTimeout, label)
				require.ErrorIs(t, apierror.HiddenCause(err), cause, label)

				e = mapper.Map(err)
				require.Equal(t, code.Code_CANCELLED, e.Code(), label) //nolint:misspell // google.rpc.Code
				noSecret(t, e)
			}
		}
	}

	// A driver timeout with both contexts alive hides the driver error as well.
	hidden := coreerrs.NewFieldError("storedField", "secret")
	err = l.mapError(t.Context(), t.Context(), fmt.Errorf("mongo: read page: %w", errors.Join(context.DeadlineExceeded, hidden)), false)
	require.ErrorIs(t, err, ErrTimeout)
	require.NotErrorIs(t, err, hidden)
	require.NotErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorIs(t, apierror.HiddenCause(err), hidden)
	noSecret(t, mapper.Map(err))
}

// TestAnd pins the conjunction of a scope and a filter.
func TestAnd(t *testing.T) {
	t.Parallel()

	scope := bson.D{{Key: "owner", Value: "a"}}
	filter := bson.D{{Key: "kind", Value: "b"}}

	require.Nil(t, and(nil, nil))
	require.Equal(t, filter, and(nil, filter))
	require.Equal(t, scope, and(scope, nil))
	require.Equal(t, bson.D{{Key: "$and", Value: bson.A{scope, filter}}}, and(scope, filter))
}

// TestNewListEngineRejectsOptions checks the options refused at construction beyond the paths and patterns.
func TestNewListEngineRejectsOptions(t *testing.T) {
	t.Parallel()

	s := listingtest.NewSchema(t)
	for name, opt := range map[string]ListEngineOption{
		"sub-millisecond precision": WithListTimestampPrecision(time.Microsecond),
		"uneven precision":          WithListTimestampPrecision(1500 * time.Microsecond),
		"negative time cap":         func(o *listOptions) { o.maxTime = -time.Second },
	} {
		_, err := NewListEngine[listingtest.Widget](nil, nil, s, opt)
		require.ErrorIs(t, err, listing.ErrInvalidSchema, name)
	}
}
