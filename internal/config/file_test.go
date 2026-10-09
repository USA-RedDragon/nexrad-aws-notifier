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

const (
	ipv4Host        = "10.0.0.1"
	ipv6Host        = "fd00::1"
	metricsIPV4Host = "10.0.0.2"
	metricsIPV6Host = "fd00::2"
	otlpEndpoint    = "collector:4317"
	corsHost        = "example.com"
	enabled         = "true"
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
		{"http.ipv4_host", h.IPV4Host, ipv4Host},
		{"http.ipv6_host", h.IPV6Host, ipv6Host},
		{"http.port", h.Port, uint16(9001)},
		{"http.tracing.enabled", h.Tracing.Enabled, true},
		{"http.tracing.otlp_endpoint", h.Tracing.OTLPEndpoint, otlpEndpoint},
		{"http.pprof.enabled", h.PProf.Enabled, true},
		{"http.metrics.enabled", h.Metrics.Enabled, true},
		{"http.metrics.ipv4_host", h.Metrics.IPV4Host, metricsIPV4Host},
		{"http.metrics.ipv6_host", h.Metrics.IPV6Host, metricsIPV6Host},
		{"http.metrics.port", h.Metrics.Port, uint16(9002)},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.key, c.got, c.want)
		}
	}
	if !slices.Equal(h.TrustedProxies, []string{"10.0.0.0/8"}) {
		t.Errorf("http.trusted_proxies = %v", h.TrustedProxies)
	}
	if !slices.Equal(h.CORSHosts, []string{corsHost}) {
		t.Errorf("http.cors_hosts = %v", h.CORSHosts)
	}
}

// The environment names predate the move to configulator and deployments set
// them, so they have to stay exactly as they were.
func TestEnvironmentNames(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"HTTP_IPV4_HOST":             ipv4Host,
		"HTTP_IPV6_HOST":             ipv6Host,
		"HTTP_PORT":                  "9001",
		"HTTP_TRUSTED_PROXIES":       "10.0.0.0/8,192.168.0.0/16",
		"HTTP_CORS_HOSTS":            corsHost,
		"HTTP_TRACING_ENABLED":       enabled,
		"HTTP_TRACING_OTLP_ENDPOINT": otlpEndpoint,
		"HTTP_PPROF_ENABLED":         enabled,
		"HTTP_METRICS_ENABLED":       enabled,
		"HTTP_METRICS_IPV4_HOST":     metricsIPV4Host,
		"HTTP_METRICS_IPV6_HOST":     metricsIPV6Host,
		"HTTP_METRICS_PORT":          "9002",
	}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	loader := config.New(fs).WithEnviron(func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	})
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}

	h := cfg.HTTP
	want := config.HTTP{
		IPV4Host:       ipv4Host,
		IPV6Host:       ipv6Host,
		Port:           9001,
		TrustedProxies: []string{"10.0.0.0/8", "192.168.0.0/16"},
		CORSHosts:      []string{corsHost},
		Tracing:        config.Tracing{Enabled: true, OTLPEndpoint: otlpEndpoint},
		PProf:          config.PProf{Enabled: true},
		Metrics:        config.Metrics{Enabled: true, IPV4Host: metricsIPV4Host, IPV6Host: metricsIPV6Host, Port: 9002},
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
