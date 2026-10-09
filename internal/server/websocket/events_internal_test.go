package websocket

import (
	"errors"
	"strings"
	"testing"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/events"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/nexrad"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/sqs"
)

const kfcx = "KFCX"

func newTestHub() *EventsHub {
	return &EventsHub{subscribers: make(map[*EventsWebsocket]struct{})}
}

func newTestSub(hub *EventsHub, messageType events.EventType, station string) *EventsWebsocket {
	sub := &EventsWebsocket{
		hub:         hub,
		events:      make(chan events.Event, subscriberBuffer),
		messageType: messageType,
		station:     station,
	}
	hub.add(sub)
	return sub
}

func received(t *testing.T, sub *EventsWebsocket) (events.Event, bool) {
	t.Helper()
	select {
	case event := <-sub.events:
		return event, true
	default:
		return nil, false
	}
}

// Every matching subscriber must get its own copy. The previous hub pushed
// each event onto one shared channel, so N connected clients load-balanced the
// stream instead of all receiving it.
func TestBroadcastReachesEverySubscriber(t *testing.T) {
	t.Parallel()
	hub := newTestHub()
	first := newTestSub(hub, events.EventTypeNexradChunk, kfcx)
	second := newTestSub(hub, events.EventTypeNexradChunk, kfcx)

	event := events.NexradChunkEvent{Station: kfcx, Chunk: "1"}
	hub.broadcast(event)

	for name, sub := range map[string]*EventsWebsocket{"first": first, "second": second} {
		got, ok := received(t, sub)
		if !ok {
			t.Errorf("%s subscriber received nothing", name)
			continue
		}
		if got != events.Event(event) {
			t.Errorf("%s subscriber got %v, want %v", name, got, event)
		}
	}
}

func TestBroadcastFilters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		messageType events.EventType
		station     string
		event       events.Event
		want        bool
	}{
		{"matching chunk", events.EventTypeNexradChunk, kfcx,
			events.NexradChunkEvent{Station: kfcx}, true},
		{"station is case insensitive", events.EventTypeNexradChunk, strings.ToLower(kfcx),
			events.NexradChunkEvent{Station: kfcx}, true},
		{"wrong station", events.EventTypeNexradChunk, kfcx,
			events.NexradChunkEvent{Station: "KTLX"}, false},
		{"wrong type", events.EventTypeNexradArchive, kfcx,
			events.NexradChunkEvent{Station: kfcx}, false},
		{"matching archive", events.EventTypeNexradArchive, kfcx,
			events.NexradArchiveEvent{Station: kfcx}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hub := newTestHub()
			sub := newTestSub(hub, tt.messageType, tt.station)
			hub.broadcast(tt.event)
			if _, ok := received(t, sub); ok != tt.want {
				t.Errorf("delivered = %v, want %v", ok, tt.want)
			}
		})
	}
}

// A client that stops reading must not wedge the hub for everyone else.
func TestBroadcastDropsForSlowSubscriber(t *testing.T) {
	t.Parallel()
	hub := newTestHub()
	slow := newTestSub(hub, events.EventTypeNexradChunk, kfcx)
	fast := newTestSub(hub, events.EventTypeNexradChunk, kfcx)

	for range subscriberBuffer + 5 {
		hub.broadcast(events.NexradChunkEvent{Station: kfcx})
		// Keep one subscriber drained so only the other overflows.
		<-fast.events
	}

	if len(slow.events) != subscriberBuffer {
		t.Errorf("slow subscriber buffered %d events, want it capped at %d",
			len(slow.events), subscriberBuffer)
	}
	if _, ok := received(t, fast); ok {
		t.Error("fast subscriber should be drained")
	}
}

func TestRemoveStopsDelivery(t *testing.T) {
	t.Parallel()
	hub := newTestHub()
	sub := newTestSub(hub, events.EventTypeNexradChunk, kfcx)

	hub.remove(sub)
	hub.broadcast(events.NexradChunkEvent{Station: kfcx})

	if _, ok := received(t, sub); ok {
		t.Error("removed subscriber still received an event")
	}
}

// The handler validates before upgrading, but a refusal from the listener must
// still close the connection rather than be logged as a policy hiccup, and
// must not leave an Unlisten owed for a station that was never registered.
func TestOnConnectRefusesUnknownStation(t *testing.T) {
	t.Parallel()
	hub := newTestHub()
	for _, messageType := range []events.EventType{events.EventTypeNexradChunk, events.EventTypeNexradArchive} {
		conn := &EventsWebsocket{hub: hub, events: make(chan events.Event, subscriberBuffer)}
		err := conn.OnConnect(t.Context(), nil, nil, messageType, "nonsense", &sqs.Listener{})
		if !errors.Is(err, nexrad.ErrUnknownStation) {
			t.Errorf("%s: OnConnect = %v, want ErrUnknownStation", messageType, err)
		}
		if conn.subscribed {
			t.Errorf("%s: refused connection is marked subscribed", messageType)
		}
	}
	if len(hub.subscribers) != 0 {
		t.Errorf("refused connections were added to the hub: %d", len(hub.subscribers))
	}
}
