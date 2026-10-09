package config_test

import (
	"errors"
	"testing"

	"github.com/USA-RedDragon/nexrad-aws-notifier/cmd"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/config"
)

const portFlag = "--http.port"

func TestExampleConfig(t *testing.T) {
	t.Parallel()
	baseCmd := cmd.NewCommand("testing", "deadbeef")
	// Avoid port conflict
	baseCmd.SetArgs([]string{"--config", "../../config.example.yaml", portFlag, "8083", "--http.metrics.port", "8084"})
	err := baseCmd.Execute()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTracing(t *testing.T) {
	t.Parallel()
	baseCmd := cmd.NewCommand("testing", "deadbeef")
	// Avoid port conflict
	baseCmd.SetArgs([]string{portFlag, "8085", "--http.metrics.port", "8086", "--http.tracing.enabled", enabled, "--http.tracing.otlp_endpoint", "127.0.0.1:1"})
	err := baseCmd.Execute()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEnvConfig(t *testing.T) {
	t.Setenv("HTTP_PORT", "8087")
	t.Setenv("HTTP_METRICS_PORT", "8088")
	baseCmd := cmd.NewCommand("testing", "deadbeef")
	err := baseCmd.Execute()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateTracing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		tracing config.Tracing
		want    error
	}{
		{"disabled", config.Tracing{}, nil},
		{"disabled with endpoint", config.Tracing{OTLPEndpoint: otlpEndpoint}, nil},
		{"enabled with endpoint", config.Tracing{Enabled: true, OTLPEndpoint: otlpEndpoint}, nil},
		{"enabled without endpoint", config.Tracing{Enabled: true}, config.ErrOTLPEndpointRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := config.Config{HTTP: config.HTTP{Tracing: tt.tracing}}.Validate()
			if !errors.Is(err, tt.want) {
				t.Errorf("Validate() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestTracingWithoutEndpointFailsToLoad(t *testing.T) {
	t.Parallel()
	baseCmd := cmd.NewCommand("testing", "deadbeef")
	baseCmd.SetArgs([]string{portFlag, "8089", "--http.tracing.enabled", enabled})
	err := baseCmd.Execute()
	if !errors.Is(err, config.ErrOTLPEndpointRequired) {
		t.Errorf("got %v, want %v", err, config.ErrOTLPEndpointRequired)
	}
}
