package config_test

import (
	"testing"

	"github.com/USA-RedDragon/nexrad-aws-notifier/cmd"
)

func TestExampleConfig(t *testing.T) {
	t.Parallel()
	baseCmd := cmd.NewCommand("testing", "deadbeef")
	// Avoid port conflict
	baseCmd.SetArgs([]string{"--config", "../../config.example.yaml", "--http.port", "8083", "--http.metrics.port", "8084"})
	err := baseCmd.Execute()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
