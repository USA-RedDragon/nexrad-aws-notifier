package cmd

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/config"
	"go.opentelemetry.io/otel"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
)

// fakeCollector is a stand-in OTLP endpoint that records the service names of
// the spans it is sent.
type fakeCollector struct {
	collectortrace.UnimplementedTraceServiceServer

	mu       sync.Mutex
	services []string
	spans    int
}

func (f *fakeCollector) Export(_ context.Context, req *collectortrace.ExportTraceServiceRequest) (*collectortrace.ExportTraceServiceResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rs := range req.GetResourceSpans() {
		for _, attr := range rs.GetResource().GetAttributes() {
			if attr.GetKey() == "service.name" {
				f.services = append(f.services, attr.GetValue().GetStringValue())
			}
		}
		for _, ss := range rs.GetScopeSpans() {
			f.spans += len(ss.GetSpans())
		}
	}
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

func startCollector(t *testing.T) (string, *fakeCollector) {
	t.Helper()
	var lc net.ListenConfig
	lis, err := lc.Listen(t.Context(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	collector := &fakeCollector{}
	srv := grpc.NewServer()
	collectortrace.RegisterTraceServiceServer(srv, collector)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String(), collector
}

func tracingConfig(endpoint string) *config.Config {
	return &config.Config{HTTP: config.HTTP{Tracing: config.Tracing{Enabled: true, OTLPEndpoint: endpoint}}}
}

// The otelgin middleware used to be the only tracing code, so spans went to
// the global no-op provider and the configured endpoint was never dialed.
//
//nolint:paralleltest // sets the global tracer provider
func TestTracingExportsToTheConfiguredEndpoint(t *testing.T) {
	endpoint, collector := startCollector(t)

	shutdown, err := setupTracing(t.Context(), tracingConfig(endpoint), "testing")
	if err != nil {
		t.Fatalf("setupTracing: %v", err)
	}

	_, span := otel.Tracer("test").Start(t.Context(), "span")
	span.End()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	collector.mu.Lock()
	defer collector.mu.Unlock()
	if collector.spans != 1 {
		t.Fatalf("collector received %d spans, want 1", collector.spans)
	}
	if len(collector.services) == 0 || collector.services[0] != serviceName {
		t.Fatalf("service.name = %v, want %q", collector.services, serviceName)
	}
}

// Nothing listens on the endpoint. The exporter dials lazily, so starting and
// stopping with no spans pending must not wait on it.
//
//nolint:paralleltest // sets the global tracer provider
func TestTracingStartsAndStopsAgainstADeadEndpoint(t *testing.T) {
	shutdown, err := setupTracing(t.Context(), tracingConfig("127.0.0.1:1"), "testing")
	if err != nil {
		t.Fatalf("setupTracing: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestTracingDisabledIsANoOp(t *testing.T) {
	t.Parallel()
	shutdown, err := setupTracing(t.Context(), &config.Config{}, "testing")
	if err != nil {
		t.Fatalf("setupTracing: %v", err)
	}
	if err := shutdown(t.Context()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
