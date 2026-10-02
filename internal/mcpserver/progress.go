package mcpserver

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/mark3labs/mcp-go/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/giantswarm/muster/v5/pkg/logging"
	"github.com/giantswarm/muster/v5/pkg/observability"
)

// Progress bridging relays a downstream server's notifications/progress to
// the client whose tool call caused them.
//
// The caller's progressToken never leaves muster. The aggregator puts a
// ProgressReporter for the caller's request on the context of the tool call;
// a client that finds one mints a token of its own for the downstream
// request, unique on its connection, and routes every progress notification
// carrying that token to the reporter, which sends it on with the caller's
// token. A connection pooled across aggregator sessions therefore serves
// each caller only its own progress. A notification whose token belongs to
// no call in flight (it arrived after the call returned, or the server made
// the token up) is dropped and counted.

// Outcomes of the muster.downstream_progress_notifications counter.
const (
	progressRelayed       = "relayed"
	progressUnknownToken  = "unknown_token"
	progressNotIncreasing = "not_increasing"
	progressSendFailed    = "send_failed"
)

// progressNotifications counts the downstream progress notifications muster
// received, by outcome. Exported via the Prometheus OTEL exporter as
// muster_downstream_progress_notifications_total. nil when the instrument
// could not be created; countProgress is nil-safe.
var progressNotifications = sync.OnceValue(func() metric.Int64Counter {
	counter, err := otel.Meter(observability.TracerName).Int64Counter("muster.downstream_progress_notifications",
		metric.WithDescription("Number of progress notifications downstream MCP servers sent the muster aggregator, by outcome."),
		metric.WithUnit("{notification}"),
	)
	if err != nil {
		logging.Warn("MCPClient", "create muster.downstream_progress_notifications counter: %v", err)
		return nil
	}
	return counter
})

func countProgress(ctx context.Context, outcome string) {
	if counter := progressNotifications(); counter != nil {
		counter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
	}
}

// ProgressReporter sends a downstream tool's progress to the caller of one
// tool call, under the progressToken the caller chose.
//
// A call that reaches several downstream tools (a workflow) shares one
// reporter, and each tool counts from its own start; the protocol requires
// the progress a caller sees to increase, so a value that does not exceed
// the last one sent is dropped and counted.
type ProgressReporter struct {
	token mcp.ProgressToken
	send  func(params map[string]any) error

	mu   sync.Mutex
	sent bool
	last float64
}

// NewProgressReporter returns a reporter for the caller's token; send
// delivers a notifications/progress with the given params to the caller.
func NewProgressReporter(token mcp.ProgressToken, send func(params map[string]any) error) *ProgressReporter {
	return &ProgressReporter{token: token, send: send}
}

// report sends one downstream progress update on with the caller's token.
func (r *ProgressReporter) report(ctx context.Context, progress float64, total *float64, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.sent && progress <= r.last {
		countProgress(ctx, progressNotIncreasing)
		return
	}

	params := map[string]any{
		"progressToken": r.token,
		"progress":      progress,
	}
	if total != nil {
		params["total"] = *total
	}
	if message != "" {
		params["message"] = message
	}
	if err := r.send(params); err != nil {
		countProgress(ctx, progressSendFailed)
		logging.Debug("MCPClient", "Progress notification not delivered to the caller: %v", err)
		return
	}
	r.sent = true
	r.last = progress
	countProgress(ctx, progressRelayed)
}

type progressReporterKey struct{}

// WithProgressReporter returns a context whose tool calls relay downstream
// progress to reporter.
func WithProgressReporter(ctx context.Context, reporter *ProgressReporter) context.Context {
	return context.WithValue(ctx, progressReporterKey{}, reporter)
}

func progressReporterFrom(ctx context.Context) *ProgressReporter {
	reporter, _ := ctx.Value(progressReporterKey{}).(*ProgressReporter)
	return reporter
}

// progressRouter maps the progress tokens a client sent downstream to the
// reporters of the calls in flight. The zero value is ready to use.
type progressRouter struct {
	next   atomic.Uint64
	mu     sync.Mutex
	routes map[string]*ProgressReporter
}

// open mints a downstream token for one call and routes its progress to
// reporter until close is called.
func (r *progressRouter) open(reporter *ProgressReporter) (token string, close func()) {
	token = fmt.Sprintf("muster-%d", r.next.Add(1))

	r.mu.Lock()
	if r.routes == nil {
		r.routes = make(map[string]*ProgressReporter)
	}
	r.routes[token] = reporter
	r.mu.Unlock()

	return token, func() {
		r.mu.Lock()
		delete(r.routes, token)
		r.mu.Unlock()
	}
}

// route hands a downstream notifications/progress to the reporter of the
// call its token belongs to.
func (r *progressRouter) route(notification mcp.JSONRPCNotification) {
	ctx := context.Background()
	fields := notification.Params.AdditionalFields

	token, _ := fields["progressToken"].(string)
	r.mu.Lock()
	reporter := r.routes[token]
	r.mu.Unlock()

	progress, ok := fields["progress"].(float64)
	if reporter == nil || !ok {
		countProgress(ctx, progressUnknownToken)
		logging.Debug("MCPClient", "Dropping progress notification for unknown token %v", fields["progressToken"])
		return
	}

	var total *float64
	if value, ok := fields["total"].(float64); ok {
		total = &value
	}
	message, _ := fields["message"].(string)
	reporter.report(ctx, progress, total, message)
}
