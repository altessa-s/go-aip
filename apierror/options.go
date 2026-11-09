// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package apierror

//go:generate optgen generate --type=errorOptions --option-type=Option --output=options_gen.go
//go:generate optgen generate --type=mapperOptions --option-type=MapperOption --output=mapper_options_gen.go
//go:generate optgen generate --type=renderOptions --option-type=RenderOption --output=render_options_gen.go

import (
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
)

// errorOptions holds the details of an [Error] built by [New].
type errorOptions struct {
	// WithReason sets the ErrorInfo reason: UPPER_SNAKE_CASE, at most 63 characters, unique within the domain. Empty
	// derives it from the code ("NOT_FOUND").
	reason string
	// WithDomain sets the ErrorInfo domain, the logical grouping the reason belongs to — usually the service name
	// ("pubsub.googleapis.com"). Empty leaves it to [Mapper] ([WithDefaultDomain]).
	domain string
	// WithMetadata sets the ErrorInfo metadata: the dynamic values of the message, keyed by lowerCamelCase keys of at
	// most 64 characters. The map is copied.
	metadata map[string]string `optval:"clone"`
	// WithFieldViolations appends BadRequest field violations: the request field path ("updateMask.paths[2]",
	// "requests[0].book.title") and what is wrong with it.
	fieldViolations []*errdetails.BadRequest_FieldViolation `optgen:"append"`
	// WithLocalizedMessage sets the LocalizedMessage detail: a BCP-47 locale ("en-US") and the message in it, both
	// required.
	localizedMessage *errdetails.LocalizedMessage `optfn:"LocalizedMessage=newLocalizedMessage"`
	// WithHelpLinks appends Help links: documentation or a console page that resolves the error.
	helpLinks []*errdetails.Help_Link `optgen:"append"`
	// WithRetryDelay sets the RetryInfo detail: how long the client waits before retrying; a delay that is not positive is ignored.
	retryDelay time.Duration
	// WithQuotaViolations appends QuotaFailure violations (usually with RESOURCE_EXHAUSTED).
	quotaViolations []*errdetails.QuotaFailure_Violation `optgen:"append"`
	// WithPreconditionViolations appends PreconditionFailure violations (usually with FAILED_PRECONDITION).
	preconditionViolations []*errdetails.PreconditionFailure_Violation `optgen:"append"`
	// WithResourceInfo sets the ResourceInfo detail: the resource the error is about.
	resourceInfo *errdetails.ResourceInfo
	// WithRequestInfo sets the RequestInfo detail: the request id to quote in a bug report.
	requestInfo *errdetails.RequestInfo
	// WithDebugInfo sets the DebugInfo detail: a stack trace or internal detail. It never leaves the server unless the
	// rendering asks for it ([WithIncludeDebugInfo]).
	debugInfo *errdetails.DebugInfo
	// WithDetails appends other detail messages; each message type may appear once, and the standard types above
	// go through their own options.
	details []proto.Message `optgen:"append"`
	// WithCause keeps the error that caused this one: reachable with errors.Is and errors.As, never rendered.
	cause error
}

// mapperOptions configures a [Mapper].
type mapperOptions struct {
	// WithRules appends rules evaluated in order before the default rules of go-atlas-core/errors; the first match
	// wins.
	rules []Rule `optgen:"append"`
	// WithDefaultDomain sets the ErrorInfo domain of every mapped error without one.
	defaultDomain string
	// WithExposeUnknown publishes the text of an error no rule matches as the INTERNAL message. Off by default: an
	// unclassified error may name internals (AIP-193), so its message is [MessageInternal].
	exposeUnknown bool `optval:"param"`
}

// renderOptions configures how an [Error] is rendered.
type renderOptions struct {
	// WithIncludeDebugInfo renders the DebugInfo detail. Off by default: debug data is for the service's own
	// developers and must not reach clients.
	includeDebugInfo bool `optval:"param"`
}

func newLocalizedMessage(locale, message string) *errdetails.LocalizedMessage {
	return &errdetails.LocalizedMessage{Locale: locale, Message: message}
}
