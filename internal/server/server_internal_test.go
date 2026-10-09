package server

import (
	"testing"
	"time"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/config"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/events"
)

// pprof streams profiles for longer than the default write timeout, so every
// listener has to get the longer one, not just the IPv4 ones.
func TestPProfWriteTimeoutCoversEveryListener(t *testing.T) {
	t.Parallel()
	eventsChannel := make(chan events.Event)
	t.Cleanup(func() { close(eventsChannel) })

	s := NewServer(&config.HTTP{
		PProf:   config.PProf{Enabled: true},
		Metrics: config.Metrics{Enabled: true},
	}, eventsChannel, nil)

	for name, timeout := range map[string]time.Duration{
		"ipv4":         s.ipv4Server.WriteTimeout,
		"ipv6":         s.ipv6Server.WriteTimeout,
		"metrics ipv4": s.metricsIPV4Server.WriteTimeout,
		"metrics ipv6": s.metricsIPV6Server.WriteTimeout,
	} {
		if timeout < 60*time.Second {
			t.Errorf("%s write timeout is %s with pprof enabled", name, timeout)
		}
	}
}
