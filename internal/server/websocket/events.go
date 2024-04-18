package websocket

import (
	"context"
	"encoding/json"
	"fmt"
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

	fmt.Println("New websocket connection for", messageType, "at", station)
	switch messageType {
	case events.EventTypeNexradChunk:
		err := sqsListener.ListenChunk(station)
		if err != nil {
			fmt.Println("Error listening to SQS:", err)
			return
		}
	case events.EventTypeNexradArchive:
		err := sqsListener.ListenArchive(station)
		if err != nil {
			fmt.Println("Error listening to SQS:", err)
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
						fmt.Println("Error casting event to NexradArchiveEvent")
						continue
					}
					if archiveEvent.Station != station || messageType != events.EventTypeNexradArchive {
						continue
					}
				case events.EventTypeNexradChunk:
					chunkEvent, ok := event.(events.NexradChunkEvent)
					if !ok {
						fmt.Println("Error casting event to NexradChunkEvent")
						continue
					}
					if chunkEvent.Station != station || messageType != events.EventTypeNexradChunk {
						continue
					}
				}
				eventDataJSON, err := json.Marshal(event)
				if err != nil {
					fmt.Println("Error marshalling event data:", err)
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

func (c *EventsWebsocket) OnDisconnect(_ context.Context, _ *http.Request) {
	c.connectedCount--
	c.cancel()
}
