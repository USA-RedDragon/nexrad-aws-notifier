package websocket

import "sync"

type Message struct {
	Type int
	Data []byte
}

type Writer interface {
	WriteMessage(message Message)
	Error(message string)
}

// wsWriter funnels messages from producer goroutines to the single goroutine
// that owns the connection's write side. Sends abandon once done is closed so
// a producer can never outlive the connection it is writing to.
type wsWriter struct {
	writer    chan Message
	error     chan string
	done      chan struct{}
	closeOnce sync.Once
}

func newWSWriter(buffer int) *wsWriter {
	return &wsWriter{
		writer: make(chan Message, buffer),
		error:  make(chan string, 1),
		done:   make(chan struct{}),
	}
}

// Close releases every goroutine blocked writing to this writer. Safe to call
// more than once.
func (w *wsWriter) Close() {
	w.closeOnce.Do(func() { close(w.done) })
}

func (w *wsWriter) WriteMessage(message Message) {
	select {
	case w.writer <- message:
	case <-w.done:
	}
}

func (w *wsWriter) Error(message string) {
	select {
	case w.error <- message:
	case <-w.done:
	}
}
