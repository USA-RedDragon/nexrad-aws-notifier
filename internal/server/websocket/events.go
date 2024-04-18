package websocket

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/events"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/sqs"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/websocket"
	gorillaWebsocket "github.com/gorilla/websocket"
)

type EventsWebsocket struct {
	websocket.Websocket
	cancel           context.CancelFunc
	websocketChannel chan events.Event
	eventsChannel    chan events.Event
	connectedCount   uint
}

func CreateEventsWebsocket(eventsChannel chan events.Event) *EventsWebsocket {
	ew := &EventsWebsocket{
		websocketChannel: make(chan events.Event),
		eventsChannel:    eventsChannel,
	}

	go ew.start()

	return ew
}

func (c *EventsWebsocket) start() {
	for {
		event := <-c.eventsChannel
		// If the websocket is closed, we just want to drop the event
		if c.connectedCount > 0 {
			c.websocketChannel <- event
		}
	}
}

func (c *EventsWebsocket) OnMessage(_ context.Context, _ *http.Request, _ websocket.Writer, _ []byte, _ int) {
}

func (c *EventsWebsocket) OnConnect(ctx context.Context, _ *http.Request, w websocket.Writer, messageType events.EventType, station string, sqsListener *sqs.Listener) {
	newCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	slog.Info("New websocket connection for", "type", messageType, "at", station)
	switch messageType {
	case events.EventTypeNexradChunk:
		err := sqsListener.ListenChunk(ctx, station)
		if err != nil {
			slog.Warn("Error listening to SQS:", "error", err)
			return
		}
	case events.EventTypeNexradArchive:
		err := sqsListener.ListenArchive(ctx, station)
		if err != nil {
			slog.Warn("Error listening to SQS:", "error", err)
			return
		}
	}

	c.connectedCount++

	go func() {
		channel := make(chan websocket.Message)
		for {
			select {
			case <-ctx.Done():
				return
			case <-newCtx.Done():
				return
			case event := <-c.websocketChannel:
				if event == nil {
					return
				}
				switch event.GetType() {
				case events.EventTypeNexradArchive:
					archiveEvent, ok := event.(events.NexradArchiveEvent)
					if !ok {
						slog.Warn("Error casting event to NexradArchiveEvent")
						continue
					}
					if archiveEvent.Station != station || messageType != events.EventTypeNexradArchive {
						continue
					}
				case events.EventTypeNexradChunk:
					chunkEvent, ok := event.(events.NexradChunkEvent)
					if !ok {
						slog.Warn("Error casting event to NexradChunkEvent")
						continue
					}
					if chunkEvent.Station != station || messageType != events.EventTypeNexradChunk {
						continue
					}
				}
				eventDataJSON, err := json.Marshal(event)
				if err != nil {
					slog.Warn("Error marshalling event data:", err)
					continue
				}
				w.WriteMessage(websocket.Message{
					Type: gorillaWebsocket.TextMessage,
					Data: eventDataJSON,
				})
			case <-channel:
				// We don't actually want to receive messages from the client
				continue
			}
		}
	}()
}

func (c *EventsWebsocket) OnDisconnect(ctx context.Context, _ *http.Request, messageType events.EventType, station string, sqsListener *sqs.Listener) {
	slog.Info("Websocket disconnected for", "type", messageType, "at", station)
	switch messageType {
	case events.EventTypeNexradChunk:
		err := sqsListener.UnlistenChunk(ctx, station)
		if err != nil {
			slog.Warn("Error stopping SQS listener:", err)
		}
	case events.EventTypeNexradArchive:
		err := sqsListener.UnlistenArchive(ctx, station)
		if err != nil {
			slog.Warn("Error stopping SQS listener:", err)
		}
	default:
		slog.Warn("Unknown event type", "type", messageType)
	}
	c.connectedCount--
	c.cancel()
}
