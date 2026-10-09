package config_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/config"
	"github.com/spf13/cobra"
)

const everyKey = `http:
  ipv4_host: 10.0.0.1
  ipv6_host: fd00::1
  port: 9001
  trusted_proxies: [10.0.0.0/8]
  cors_hosts: [example.com]
  tracing:
    enabled: true
    otlp_endpoint: collector:4317
  pprof:
    enabled: true
  metrics:
    enabled: true
    ipv4_host: 10.0.0.2
    ipv6_host: fd00::2
    port: 9002
`

// Every key config.example.yaml documents has to reach the struct. The structs
// once carried only json tags, which the YAML decoder ignores, so only the
// single-word keys loaded and the rest silently kept their defaults.
func TestFileLoadsEveryDocumentedKey(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(everyKey), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	config.RegisterFlags(cmd)
	if err := cmd.Flags().Set("config", path); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(cmd)
	if err != nil {
		t.Fatal(err)
	}

	h := cfg.HTTP
	for _, c := range []struct {
		key       string
		got, want any
	}{
		{"http.ipv4_host", h.IPV4Host, "10.0.0.1"},
		{"http.ipv6_host", h.IPV6Host, "fd00::1"},
		{"http.port", h.Port, uint16(9001)},
		{"http.tracing.enabled", h.Tracing.Enabled, true},
		{"http.tracing.otlp_endpoint", h.Tracing.OTLPEndpoint, "collector:4317"},
		{"http.pprof.enabled", h.PProf.Enabled, true},
		{"http.metrics.enabled", h.Metrics.Enabled, true},
		{"http.metrics.ipv4_host", h.Metrics.IPV4Host, "10.0.0.2"},
		{"http.metrics.ipv6_host", h.Metrics.IPV6Host, "fd00::2"},
		{"http.metrics.port", h.Metrics.Port, uint16(9002)},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.key, c.got, c.want)
		}
	}
	if !slices.Equal(h.TrustedProxies, []string{"10.0.0.0/8"}) {
		t.Errorf("http.trusted_proxies = %v", h.TrustedProxies)
	}
	if !slices.Equal(h.CORSHosts, []string{"example.com"}) {
		t.Errorf("http.cors_hosts = %v", h.CORSHosts)
	}
}
