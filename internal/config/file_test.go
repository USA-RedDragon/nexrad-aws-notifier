package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/config"
	"github.com/spf13/pflag"
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

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	loader := config.New(fs)
	if err := fs.Parse([]string{"--config", path}); err != nil {
		t.Fatal(err)
	}
	cfg, err := loader.Load()
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

// The environment names predate the move to configulator and deployments set
// them, so they have to stay exactly as they were.
//
//nolint:paralleltest
func TestEnvironmentNames(t *testing.T) {
	env := map[string]string{
		"HTTP_IPV4_HOST":             "10.0.0.1",
		"HTTP_IPV6_HOST":             "fd00::1",
		"HTTP_PORT":                  "9001",
		"HTTP_TRUSTED_PROXIES":       "10.0.0.0/8,192.168.0.0/16",
		"HTTP_CORS_HOSTS":            "example.com",
		"HTTP_TRACING_ENABLED":       "true",
		"HTTP_TRACING_OTLP_ENDPOINT": "collector:4317",
		"HTTP_PPROF_ENABLED":         "true",
		"HTTP_METRICS_ENABLED":       "true",
		"HTTP_METRICS_IPV4_HOST":     "10.0.0.2",
		"HTTP_METRICS_IPV6_HOST":     "fd00::2",
		"HTTP_METRICS_PORT":          "9002",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	loader := config.New(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}

	h := cfg.HTTP
	want := config.HTTP{
		IPV4Host:       "10.0.0.1",
		IPV6Host:       "fd00::1",
		Port:           9001,
		TrustedProxies: []string{"10.0.0.0/8", "192.168.0.0/16"},
		CORSHosts:      []string{"example.com"},
		Tracing:        config.Tracing{Enabled: true, OTLPEndpoint: "collector:4317"},
		PProf:          config.PProf{Enabled: true},
		Metrics:        config.Metrics{Enabled: true, IPV4Host: "10.0.0.2", IPV6Host: "fd00::2", Port: 9002},
	}
	if !reflect.DeepEqual(h, want) {
		t.Errorf("got  %+v\nwant %+v", h, want)
	}
}

func TestConfigShorthand(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("http:\n  port: 9001\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	loader := config.New(fs)
	if err := fs.Parse([]string{"-c", path}); err != nil {
		t.Fatal(err)
	}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Port != 9001 {
		t.Errorf("http.port = %d, want 9001", cfg.HTTP.Port)
	}
}
