package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/events"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/sqs"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/websocket"
	gorillaWebsocket "github.com/gorilla/websocket"
)

// subscriberBuffer bounds how far behind a slow client may fall before its
// events start being dropped instead of stalling the hub.
const subscriberBuffer = 16

// EventsHub fans events from the SQS listener out to every connected client.
// One hub is shared by the route; each connection gets its own EventsWebsocket.
type EventsHub struct {
	eventsChannel chan events.Event

	mu          sync.RWMutex
	subscribers map[*EventsWebsocket]struct{}
}

func NewEventsHub(eventsChannel chan events.Event) *EventsHub {
	hub := &EventsHub{
		eventsChannel: eventsChannel,
		subscribers:   make(map[*EventsWebsocket]struct{}),
	}
	go hub.run()
	return hub
}

// NewConnection returns a handler scoped to a single websocket connection.
func (h *EventsHub) NewConnection() websocket.Websocket {
	return &EventsWebsocket{
		hub:    h,
		events: make(chan events.Event, subscriberBuffer),
	}
}

func (h *EventsHub) run() {
	for event := range h.eventsChannel {
		h.broadcast(event)
	}
}

// broadcast delivers to every interested subscriber. Each has its own buffer,
// so one slow client cannot starve the others of events.
func (h *EventsHub) broadcast(event events.Event) {
	if event == nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for sub := range h.subscribers {
		if !sub.wants(event) {
			continue
		}
		select {
		case sub.events <- event:
		default:
			slog.Warn("Dropping event for slow websocket client",
				"type", sub.messageType, "station", sub.station)
		}
	}
}

func (h *EventsHub) add(sub *EventsWebsocket) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subscribers[sub] = struct{}{}
}

func (h *EventsHub) remove(sub *EventsWebsocket) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subscribers, sub)
}

// EventsWebsocket serves exactly one websocket connection. Its filter fields
// are written in OnConnect before the hub can see it and read by the hub only
// while it is registered, so the hub's mutex covers them.
type EventsWebsocket struct {
	hub    *EventsHub
	events chan events.Event

	messageType events.EventType
	station     string

	cancel     context.CancelFunc
	subscribed bool
}

func (c *EventsWebsocket) wants(event events.Event) bool {
	if event.GetType() != c.messageType {
		return false
	}
	switch e := event.(type) {
	case events.NexradArchiveEvent:
		return strings.EqualFold(e.Station, c.station)
	case events.NexradChunkEvent:
		return strings.EqualFold(e.Station, c.station)
	default:
		return false
	}
}

func (c *EventsWebsocket) OnMessage(_ context.Context, _ *http.Request, _ websocket.Writer, _ []byte, _ int) {
}

func (c *EventsWebsocket) OnConnect(ctx context.Context, _ *http.Request, w websocket.Writer, messageType events.EventType, station string, sqsListener *sqs.Listener) error {
	c.messageType = messageType
	c.station = station

	switch messageType {
	case events.EventTypeNexradChunk:
		if err := sqsListener.ListenChunk(ctx, station); err != nil {
			return fmt.Errorf("failed to listen for chunk events: %w", err)
		}
	case events.EventTypeNexradArchive:
		if err := sqsListener.ListenArchive(ctx, station); err != nil {
			return fmt.Errorf("failed to listen for archive events: %w", err)
		}
	default:
		return fmt.Errorf("unknown event type %q", messageType)
	}
	// Only now is an Unlisten owed, so only now may OnDisconnect do work.
	c.subscribed = true

	slog.Info("New websocket connection", "type", messageType, "station", station)

	sendCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.hub.add(c)

	go func() {
		for {
			select {
			case <-sendCtx.Done():
				return
			case event := <-c.events:
				eventDataJSON, err := json.Marshal(event)
				if err != nil {
					slog.Warn("Error marshalling event data", "error", err)
					continue
				}
				w.WriteMessage(websocket.Message{
					Type: gorillaWebsocket.TextMessage,
					Data: eventDataJSON,
				})
			}
		}
	}()

	return nil
}

func (c *EventsWebsocket) OnDisconnect(ctx context.Context, _ *http.Request, messageType events.EventType, station string, sqsListener *sqs.Listener) {
	if !c.subscribed {
		return
	}
	c.subscribed = false

	// Stop receiving before unsubscribing so the send goroutine cannot outlive
	// the connection.
	c.hub.remove(c)
	c.cancel()

	slog.Info("Websocket disconnected", "type", messageType, "station", station)

	var err error
	switch messageType {
	case events.EventTypeNexradChunk:
		err = sqsListener.UnlistenChunk(ctx, station)
	case events.EventTypeNexradArchive:
		err = sqsListener.UnlistenArchive(ctx, station)
	}
	if err != nil {
		slog.Warn("Error stopping SQS listener", "error", err)
	}
}
