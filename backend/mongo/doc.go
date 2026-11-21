// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package mongo is the MongoDB backend of go-aip: the filter, order and projection translators, the storage schema shared by
// the filter translator and the evaluator's MongoDB profile, the list engine of the listing contract, the evaluator's value
// encoding of the driver and the BSON codecs of the go-aip and go-atlas-core value types. It is a separate Go module, released
// together with go-aip, so the MongoDB driver stays out of the root module.
//
// Schema: NewSchema(opts...) captures omission (WithSchemaOmitEmpty), zero-when-absent (WithSchemaZeroWhenAbsent), explicit
// presence (WithSchemaPresenceFields) and resolved-size (WithSchemaSizeZeroWhenAbsent) rules independently of the MongoDB driver.
// Pass the same schema to NewFilterTranslatorWithSchema and evaluator.WithProfile so validation and execution agree.
// CheckSizeOperand validates a size() operand against MongoDB's 32-bit $size bound.
//
// Filter: NewFilterTranslator takes the shared *filter.Policy (field authorization, types, limits, decimal conversion, value
// codecs and regex rules, in API names, built once per resource) and FilterOption values; WithFilterOptions binds the policy to
// storage names (filter.WithFieldMapping). Translate renders a bson.M query document, TranslateD the same filter as an ordered
// bson.D with sorted keys. A nil policy fails with filter.ErrPolicyRequired.
//
// Order: NewOrderTranslator binds the shared orderby.Policy to the storage options of WithOrderOptions and renders a bson.D sort
// document, which keeps key precedence.
//
// Projection: NewProjectionTranslator binds the shared projection.Policy to the storage options of WithProjectionOptions and
// renders a bson.M inclusion or exclusion projection; TranslateView applies an AIP-157 view when the read mask is omitted.
//
// List engine: NewListEngine[D](collection, signer, schema, opts...) serves listing.Engine[D, bson.D] with keyset pages read by a
// plain find; the request filter is translated per call and ANDed with the caller's scope, whose keys (WithListScopeKeys) are
// pinned by equality and one of which may be bound to Request.Parent (WithListParentKey). Indexes and EnsureIndexes declare and
// create the compound index of every sort pattern. The engine uses go-mongox for keyset pagination and create-only index management.
//
// Value encoding: ValueEncoding returns the evaluator.ValueEncoding of the driver, so an in-memory evaluator reads a bson.D, a
// struct or a value with a custom BSON encoding as the document the driver stores.
//
// Codecs: NewRegistry returns a BSON registry storing optional.Optional values, exact Decimal128 decimals and calendar dates;
// RegisterDateType adds the date codec of another date type.
//
// Every option is prefixed with its kind: WithFilter…, WithOrder…, WithProjection…, WithSchema…, WithList…, WithValueEncoding…,
// WithDate…. All translators, schemas, list engines and encodings are safe for concurrent use after construction.
//
// # Usage
//
//	schema, err := mongo.NewSchema(
//	    mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"retries": filter.FieldKindInt}),
//	    mongo.WithSchemaOmitEmpty("description"),
//	)
//	if err != nil {
//	    return err
//	}
//	policy, err := filter.NewPolicy(filter.WithAllowedFields("name", "retries", "description"))
//	if err != nil {
//	    return err
//	}
//	where, err := mongo.NewFilterTranslatorWithSchema(policy, schema)
//	if err != nil {
//	    return err
//	}
//	query, _ := where.Translate(ast) // {"$and": [...]}
//
//	order, _ := mongo.NewOrderTranslator(orderPolicy)
//	sort, _ := order.Translate(spec) // bson.D{{"create_time", -1}, {"slug", 1}}
//
//	fields, _ := mongo.NewProjectionTranslator(projectionPolicy,
//	    mongo.WithProjectionOptions(projection.WithRequiredFields("_id")))
//	proj, _ := fields.Translate(mask) // bson.M{"_id": 1, "name": 1}
//
//	list, _ := mongo.NewListEngine[widget](coll, signer, listSchema,
//	    mongo.WithListFieldPaths(map[string]string{"id": "_id"}))
//	_ = list.EnsureIndexes(ctx)
//	page, _ := list.List(ctx, method, req, nil)
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package mongo
