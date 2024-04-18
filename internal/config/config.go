package config

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

type Config struct {
	HTTP HTTP `json:"http"`
}

type HTTPListener struct {
	IPV4Host string `json:"ipv4_host"`
	IPV6Host string `json:"ipv6_host"`
	Port     uint16 `json:"port"`
}

type Tracing struct {
	Enabled      bool   `json:"enabled"`
	OTLPEndpoint string `json:"otlp_endpoint"`
}

type PProf struct {
	Enabled bool `json:"enabled"`
}

type Metrics struct {
	HTTPListener
	Enabled bool `json:"enabled"`
}

type HTTP struct {
	HTTPListener
	Tracing
	PProf          PProf    `json:"pprof"`
	TrustedProxies []string `json:"trusted_proxies"`
	Metrics        Metrics  `json:"metrics"`
	CORSHosts      []string `json:"cors_hosts"`
}

//nolint:golint,gochecknoglobals
var (
	ConfigFileKey          = "config"
	HTTPIPV4HostKey        = "http.ipv4_host"
	HTTPIPV6HostKey        = "http.ipv6_host"
	HTTPPortKey            = "http.port"
	HTTPTracingEnabledKey  = "http.tracing.enabled"
	HTTPTracingOTLPEndKey  = "http.tracing.otlp_endpoint"
	HTTPPProfEnabledKey    = "http.pprof.enabled"
	HTTPTrustedProxiesKey  = "http.trusted_proxies"
	HTTPMetricsEnabledKey  = "http.metrics.enabled"
	HTTPMetricsIPV4HostKey = "http.metrics.ipv4_host"
	HTTPMetricsIPV6HostKey = "http.metrics.ipv6_host"
	HTTPMetricsPortKey     = "http.metrics.port"
	HTTPCORSHostsKey       = "http.cors_hosts"
)

const (
	DefaultHTTPIPV4Host        = "0.0.0.0"
	DefaultHTTPIPV6Host        = "::"
	DefaultHTTPPort            = 8080
	DefaultHTTPMetricsIPV4Host = "127.0.0.1"
	DefaultHTTPMetricsIPV6Host = "::1"
	DefaultHTTPMetricsPort     = 8081
)

func RegisterFlags(cmd *cobra.Command) {
	cmd.Flags().StringP(ConfigFileKey, "c", "", "Config file path")
	cmd.Flags().String(HTTPIPV4HostKey, DefaultHTTPIPV4Host, "HTTP server IPv4 host")
	cmd.Flags().String(HTTPIPV6HostKey, DefaultHTTPIPV6Host, "HTTP server IPv6 host")
	cmd.Flags().Uint16(HTTPPortKey, DefaultHTTPPort, "HTTP server port")
	cmd.Flags().Bool(HTTPTracingEnabledKey, false, "Enable Open Telemetry tracing")
	cmd.Flags().String(HTTPTracingOTLPEndKey, "", "Open Telemetry endpoint")
	cmd.Flags().Bool(HTTPPProfEnabledKey, false, "Enable pprof")
	cmd.Flags().StringSlice(HTTPTrustedProxiesKey, []string{}, "Comma-separated list of trusted proxies")
	cmd.Flags().Bool(HTTPMetricsEnabledKey, false, "Enable metrics server")
	cmd.Flags().String(HTTPMetricsIPV4HostKey, DefaultHTTPMetricsIPV4Host, "Metrics server IPv4 host")
	cmd.Flags().String(HTTPMetricsIPV6HostKey, DefaultHTTPMetricsIPV6Host, "Metrics server IPv6 host")
	cmd.Flags().Uint16(HTTPMetricsPortKey, DefaultHTTPMetricsPort, "Metrics server port")
	cmd.Flags().StringSlice(HTTPCORSHostsKey, []string{}, "Comma-separated list of CORS hosts")
}

func (c *Config) Validate() error {
	return nil
}

func LoadConfig(cmd *cobra.Command) (*Config, error) {
	var config Config

	// Load flags from envs
	ctx, cancel := context.WithCancelCause(cmd.Context())
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if ctx.Err() != nil {
			return
		}
		optName := strings.ReplaceAll(strings.ReplaceAll(strings.ToUpper(f.Name), "-", "_"), ".", "__")
		if val, ok := os.LookupEnv(optName); !f.Changed && ok {
			if err := f.Value.Set(val); err != nil {
				cancel(err)
			}
			f.Changed = true
		}
	})
	if ctx.Err() != nil {
		return &config, fmt.Errorf("failed to load env: %w", context.Cause(ctx))
	}

	configPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return &config, fmt.Errorf("failed to get config path: %w", err)
	}
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return &config, fmt.Errorf("failed to read config: %w", err)
		}

		if err := yaml.Unmarshal(data, &config); err != nil {
			return &config, fmt.Errorf("failed to unmarshal config: %w", err)
		}
	}

	err = overrideFlags(&config, cmd)
	if err != nil {
		return &config, fmt.Errorf("failed to override flags: %w", err)
	}

	// Defaults
	if config.HTTP.IPV4Host == "" {
		config.HTTP.IPV4Host = DefaultHTTPIPV4Host
	}
	if config.HTTP.IPV6Host == "" {
		config.HTTP.IPV6Host = DefaultHTTPIPV6Host
	}
	if config.HTTP.Port == 0 {
		config.HTTP.Port = DefaultHTTPPort
	}
	if config.HTTP.Metrics.IPV4Host == "" {
		config.HTTP.Metrics.IPV4Host = DefaultHTTPMetricsIPV4Host
	}
	if config.HTTP.Metrics.IPV6Host == "" {
		config.HTTP.Metrics.IPV6Host = DefaultHTTPMetricsIPV6Host
	}
	if config.HTTP.Metrics.Port == 0 {
		config.HTTP.Metrics.Port = DefaultHTTPMetricsPort
	}

	err = config.Validate()
	if err != nil {
		return &config, fmt.Errorf("failed to validate config: %w", err)
	}

	return &config, nil
}

func overrideFlags(config *Config, cmd *cobra.Command) error {
	var err error
	if cmd.Flags().Changed(HTTPIPV4HostKey) {
		config.HTTP.IPV4Host, err = cmd.Flags().GetString(HTTPIPV4HostKey)
		if err != nil {
			return fmt.Errorf("failed to get HTTP IPv4 host: %w", err)
		}
	}

	if cmd.Flags().Changed(HTTPIPV6HostKey) {
		config.HTTP.IPV6Host, err = cmd.Flags().GetString(HTTPIPV6HostKey)
		if err != nil {
			return fmt.Errorf("failed to get HTTP IPv6 host: %w", err)
		}
	}

	if cmd.Flags().Changed(HTTPPortKey) {
		config.HTTP.Port, err = cmd.Flags().GetUint16(HTTPPortKey)
		if err != nil {
			return fmt.Errorf("failed to get HTTP port: %w", err)
		}
	}

	if cmd.Flags().Changed(HTTPPProfEnabledKey) {
		config.HTTP.PProf.Enabled, err = cmd.Flags().GetBool(HTTPPProfEnabledKey)
		if err != nil {
			return fmt.Errorf("failed to get pprof enabled: %w", err)
		}
	}

	if cmd.Flags().Changed(HTTPTrustedProxiesKey) {
		config.HTTP.TrustedProxies, err = cmd.Flags().GetStringSlice(HTTPTrustedProxiesKey)
		if err != nil {
			return fmt.Errorf("failed to get trusted proxies: %w", err)
		}
	}

	if cmd.Flags().Changed(HTTPMetricsEnabledKey) {
		config.HTTP.Metrics.Enabled, err = cmd.Flags().GetBool(HTTPMetricsEnabledKey)
		if err != nil {
			return fmt.Errorf("failed to get metrics enabled: %w", err)
		}
	}

	if cmd.Flags().Changed(HTTPMetricsIPV4HostKey) {
		config.HTTP.Metrics.IPV4Host, err = cmd.Flags().GetString(HTTPMetricsIPV4HostKey)
		if err != nil {
			return fmt.Errorf("failed to get metrics IPv4 host: %w", err)
		}
	}

	if cmd.Flags().Changed(HTTPMetricsIPV6HostKey) {
		config.HTTP.Metrics.IPV6Host, err = cmd.Flags().GetString(HTTPMetricsIPV6HostKey)
		if err != nil {
			return fmt.Errorf("failed to get metrics IPv6 host: %w", err)
		}
	}

	if cmd.Flags().Changed(HTTPMetricsPortKey) {
		config.HTTP.Metrics.Port, err = cmd.Flags().GetUint16(HTTPMetricsPortKey)
		if err != nil {
			return fmt.Errorf("failed to get metrics port: %w", err)
		}
	}

	if cmd.Flags().Changed(HTTPTracingEnabledKey) {
		config.HTTP.Tracing.Enabled, err = cmd.Flags().GetBool(HTTPTracingEnabledKey)
		if err != nil {
			return fmt.Errorf("failed to get tracing enabled: %w", err)
		}
	}

	if cmd.Flags().Changed(HTTPTracingOTLPEndKey) {
		config.HTTP.Tracing.OTLPEndpoint, err = cmd.Flags().GetString(HTTPTracingOTLPEndKey)
		if err != nil {
			return fmt.Errorf("failed to get tracing OTLP endpoint: %w", err)
		}
	}

	if cmd.Flags().Changed(HTTPCORSHostsKey) {
		config.HTTP.CORSHosts, err = cmd.Flags().GetStringSlice(HTTPCORSHostsKey)
		if err != nil {
			return fmt.Errorf("failed to get CORS hosts: %w", err)
		}
	}

	return nil
}
