package sqs

import (
	"testing"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/events"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/nexrad"
)

// The value counts above are what SNS was measured to accept; this asks SNS
// itself, at each limit and with every station subscribed at once.
func TestSNSAcceptsTheFiltersForEveryStation(t *testing.T) {
	t.Parallel()
	listener, err := NewListener(make(chan events.Event))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Stop(); err != nil {
			t.Errorf("stop: %v", err)
		}
	})

	all := nexrad.Stations()
	// The most the chunk policy names individually, then every station.
	for _, n := range []int{maxChunkFilterValues, len(all)} {
		listener.chunkSites.Clear()
		listener.archiveSites.Clear()
		for _, station := range all[:n] {
			listen(listener.chunkSites, station)
			listen(listener.archiveSites, station)
		}
		if err := listener.updateChunkFilterPolicy(t.Context()); err != nil {
			t.Errorf("chunk filter for %d stations refused: %v", n, err)
		}
		if err := listener.updateArchiveFilterPolicy(t.Context()); err != nil {
			t.Errorf("archive filter for %d stations refused: %v", n, err)
		}
	}
}
