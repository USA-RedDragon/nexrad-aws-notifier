package websocket

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/config"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/events"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/sqs"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	bufferSize = 1024
	// writeWait bounds how long the close handshake may take.
	writeWait = 5 * time.Second
	// teardownTimeout bounds the SQS unsubscribe performed on disconnect.
	teardownTimeout = 10 * time.Second
)

type Websocket interface {
	OnMessage(ctx context.Context, r *http.Request, w Writer, msg []byte, t int)
	// OnConnect prepares the connection. Returning an error aborts it, and
	// OnDisconnect will not run, so subscriptions stay balanced.
	OnConnect(ctx context.Context, r *http.Request, w Writer, messageType events.EventType, station string, sqsListener *sqs.Listener) error
	OnDisconnect(ctx context.Context, r *http.Request, messageType events.EventType, station string, sqsListener *sqs.Listener)
}

type WSHandler struct {
	wsUpgrader websocket.Upgrader
	// newHandler builds per-connection state. The gin handler is registered
	// once but serves every connection concurrently, so nothing connection
	// scoped may live on WSHandler itself.
	newHandler func() Websocket
}

// OriginAllowed reports whether an Origin header matches one of the configured
// CORS hosts. A configured host of "*" allows any origin. Hosts may be given as
// a bare host, a host:port, or a full URL; an entry without a port matches the
// origin on any port.
func OriginAllowed(origin string, corsHosts []string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	originHost := strings.ToLower(parsed.Hostname())
	originPort := parsed.Port()
	if originPort == "" {
		// Fill in the scheme's default so that https://example.com matches a
		// configured example.com:443.
		switch strings.ToLower(parsed.Scheme) {
		case "https", "wss":
			originPort = "443"
		case "http", "ws":
			originPort = "80"
		}
	}

	for _, host := range corsHosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "*" {
			return true
		}
		// Accept a full URL by dropping the scheme and anything past the host.
		if scheme := strings.Index(host, "://"); scheme != -1 {
			host = host[scheme+len("://"):]
		}
		host = strings.SplitN(host, "/", 2)[0]

		wantHost, wantPort, err := net.SplitHostPort(host)
		if err != nil {
			// No port configured, so match on host alone.
			if strings.Trim(host, "[]") == originHost {
				return true
			}
			continue
		}
		if wantHost == originHost && wantPort == originPort {
			return true
		}
	}
	return false
}

func CreateHandler(newHandler func() Websocket, config *config.HTTP) func(*gin.Context) {
	handler := &WSHandler{
		wsUpgrader: websocket.Upgrader{
			HandshakeTimeout: 0,
			ReadBufferSize:   bufferSize,
			WriteBufferSize:  bufferSize,
			WriteBufferPool:  nil,
			Subprotocols:     []string{},
			Error: func(w http.ResponseWriter, r *http.Request, status int, reason error) {
				slog.Warn("Websocket handshake failed",
					"status", status, "reason", reason,
					"origin", r.Header.Get("Origin"), "remote", r.RemoteAddr,
					// The key is a per-connection nonce, not a secret, and a
					// malformed one is the most common handshake failure.
					"sec_websocket_key", r.Header.Values("Sec-Websocket-Key"))
				w.Header().Set("Sec-Websocket-Version", "13")
				http.Error(w, http.StatusText(status), status)
			},
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					// Non-browser clients omit Origin entirely.
					return true
				}
				return OriginAllowed(origin, config.CORSHosts)
			},
			EnableCompression: true,
		},
		newHandler: newHandler,
	}

	return func(c *gin.Context) {
		// Validate before upgrading so a bad request gets a real HTTP status
		// rather than a websocket that closes immediately.
		messageType := events.EventType(c.Param("type"))
		station := c.Param("station")
		if messageType == "" || station == "" {
			c.String(http.StatusBadRequest, "type and station are required")
			return
		}
		sqsListener, ok := c.MustGet("sqsListener").(*sqs.Listener)
		if !ok {
			slog.Error("Failed to get sqsListener")
			c.String(http.StatusInternalServerError, "SQS listener unavailable")
			return
		}

		conn, err := handler.wsUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			// The Error hook above has already written the response.
			slog.Error("Failed to set websocket upgrade", "error", err)
			return
		}
		defer func() { _ = conn.Close() }()

		connHandler := handler.newHandler()
		defer func() {
			// The request context is cancelled the moment this handler
			// returns, but unsubscribing from SQS still needs a live one.
			ctx, cancel := context.WithTimeout(
				context.WithoutCancel(c.Request.Context()), teardownTimeout)
			defer cancel()
			connHandler.OnDisconnect(ctx, c.Request, messageType, station, sqsListener)
		}()

		handle(c.Request.Context(), conn, connHandler, c.Request, messageType, station, sqsListener)
	}
}

func handle(ctx context.Context, conn *websocket.Conn, handler Websocket, r *http.Request, messageType events.EventType, station string, sqsListener *sqs.Listener) {
	writer := newWSWriter(bufferSize)
	// Unblocks the reader goroutine below once this function returns.
	defer writer.Close()

	if err := handler.OnConnect(ctx, r, writer, messageType, station, sqsListener); err != nil {
		slog.Warn("Websocket connect failed", "error", err, "type", messageType, "station", station)
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "failed to subscribe"),
			time.Now().Add(writeWait))
		return
	}

	go func() {
		for {
			t, msg, err := conn.ReadMessage()
			if err != nil {
				writer.Error("read failed")
				return
			}
			switch {
			case t == websocket.PingMessage:
				writer.WriteMessage(Message{
					Type: websocket.PongMessage,
				})
			case strings.EqualFold(string(msg), "ping"):
				writer.WriteMessage(Message{
					Type: websocket.TextMessage,
					Data: []byte("PONG"),
				})
			default:
				handler.OnMessage(ctx, r, writer, msg, t)
			}
		}
	}()

	// All writes to conn happen here, on one goroutine, because gorilla
	// connections do not support concurrent writers.
	for {
		select {
		case <-ctx.Done():
			return
		case <-writer.error:
			return
		case msg := <-writer.writer:
			if err := conn.WriteMessage(msg.Type, msg.Data); err != nil {
				return
			}
		}
	}
}
