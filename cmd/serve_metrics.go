package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/giantswarm/muster/v5/internal/listenfds"
)

// OTEL_METRICS_EXPORTER=prometheus-inherited is autoexport's prometheus
// exporter on the inherited listener named metrics: the test harness binds
// the exporter's port and hands it down, so muster serve never binds a port
// the harness closed first (see listenfds).
func init() {
	autoexport.RegisterMetricReader(listenfds.PrometheusExporter, newInheritedPrometheusReader)
}

func newInheritedPrometheusReader(context.Context) (sdkmetric.Reader, error) {
	listeners, err := listenfds.Take(listenfds.Metrics)
	if err != nil {
		return nil, err
	}
	if len(listeners) != 1 {
		return nil, fmt.Errorf("%s needs one inherited listener named %q, got %d",
			listenfds.PrometheusExporter, listenfds.Metrics, len(listeners))
	}
	return servePrometheus(listeners[0])
}

// servePrometheus serves an isolated registry's /metrics on ln, configured as
// autoexport's prometheus exporter is.
func servePrometheus(ln net.Listener) (sdkmetric.Reader, error) {
	reg := prometheus.NewRegistry()
	reader, err := promexporter.New(promexporter.WithRegisterer(reg))
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	server := &http.Server{
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			otel.Handle(fmt.Errorf("the Prometheus HTTP server exited unexpectedly: %w", err))
		}
	}()
	return readerWithServer{Reader: reader, server: server}, nil
}

// readerWithServer shuts the /metrics server down with the reader.
type readerWithServer struct {
	sdkmetric.Reader
	server *http.Server
}

func (r readerWithServer) Shutdown(ctx context.Context) error {
	return errors.Join(r.Reader.Shutdown(ctx), r.server.Shutdown(ctx))
}
