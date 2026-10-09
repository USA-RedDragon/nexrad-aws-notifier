package config

//go:generate go tool configulator -type Config

type Config struct {
	HTTP HTTP `name:"http" description:"HTTP server configuration"`
}

type Tracing struct {
	Enabled      bool   `name:"enabled" description:"Enable OpenTelemetry tracing"`
	OTLPEndpoint string `name:"otlp_endpoint" description:"The OpenTelemetry collector endpoint"`
}

type PProf struct {
	Enabled bool `name:"enabled" description:"Enable Go pprof"`
}

type Metrics struct {
	Enabled  bool   `name:"enabled" description:"Enable the Prometheus metrics server"`
	IPV4Host string `name:"ipv4_host" default:"127.0.0.1" description:"The IPv4 address to bind the Prometheus metrics server to. 127.0.0.1 is localhost"`
	IPV6Host string `name:"ipv6_host" default:"::1" description:"The IPv6 address to bind the Prometheus metrics server to. ::1 is localhost"`
	Port     uint16 `name:"port" default:"8081" description:"The port to bind the Prometheus metrics server to. IPv4 and IPv6 share it"`
}

type HTTP struct {
	IPV4Host       string   `name:"ipv4_host" default:"0.0.0.0" description:"The IPv4 address to bind the HTTP server to. 0.0.0.0 is all interfaces"`
	IPV6Host       string   `name:"ipv6_host" default:"::" description:"The IPv6 address to bind the HTTP server to. :: is all interfaces"`
	Port           uint16   `name:"port" default:"8080" description:"The port to bind the HTTP server to. IPv4 and IPv6 share it"`
	TrustedProxies []string `name:"trusted_proxies" description:"IP addresses or CIDR ranges of reverse proxies trusted to set the X-Forwarded-For header"`
	CORSHosts      []string `name:"cors_hosts" description:"Origins allowed to open a websocket. An entry may be a bare host (example.com, matching any port), a host:port (example.com:8080) or a full URL (https://example.com). * allows any origin"`
	Tracing        Tracing  `name:"tracing" description:"OpenTelemetry configuration"`
	PProf          PProf    `name:"pprof" description:"Go pprof configuration"`
	Metrics        Metrics  `name:"metrics" description:"Prometheus metrics configuration"`
}

func (c Config) Validate() error {
	return nil
}
