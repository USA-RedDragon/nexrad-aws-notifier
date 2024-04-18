package events

type EventType string

const (
	EventTypeNexradChunk   EventType = "nexrad-chunk"
	EventTypeNexradArchive EventType = "nexrad-archive"
)

type Event interface {
	GetType() EventType
}

type NexradChunkEvent struct {
	Station   string `json:"station"`
	Volume    string `json:"volume"`
	Chunk     string `json:"chunk"`
	ChunkType string `json:"chunkType"`
	L2Version string `json:"l2Version"`
}

func (e NexradChunkEvent) GetType() EventType {
	return EventTypeNexradChunk
}

type NexradArchiveEvent struct {
	Station string `json:"station"`
	Path    string `json:"path"`
}

func (e NexradArchiveEvent) GetType() EventType {
	return EventTypeNexradArchive
}

type EventBus struct {
	eventQueue chan Event
}

func NewEventBus() *EventBus {
	return &EventBus{
		eventQueue: make(chan Event, 100),
	}
}

func (eb *EventBus) GetChannel() chan Event {
	return eb.eventQueue
}

func (eb *EventBus) Close() {
	close(eb.eventQueue)
}
