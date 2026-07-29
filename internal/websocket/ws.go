package websocket

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/config"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/events"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/sqs"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const bufferSize = 1024

type Websocket interface {
	OnMessage(ctx context.Context, r *http.Request, w Writer, msg []byte, t int)
	OnConnect(ctx context.Context, r *http.Request, w Writer, messageType events.EventType, station string, sqsListener *sqs.Listener)
	OnDisconnect(ctx context.Context, r *http.Request, messageType events.EventType, station string, sqsListener *sqs.Listener)
}

type WSHandler struct {
	wsUpgrader websocket.Upgrader
	handler    Websocket
	conn       *websocket.Conn
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

func CreateHandler(ws Websocket, config *config.HTTP) func(*gin.Context) {
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
					"origin", r.Header.Get("Origin"), "remote", r.RemoteAddr)
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
		handler: ws,
	}

	return func(c *gin.Context) {
		conn, err := handler.wsUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			slog.Error("Failed to set websocket upgrade", "error", err)
			return
		}
		handler.conn = conn

		messageType := events.EventType(c.Param("type"))
		station := c.Param("station")
		if messageType == "" || station == "" {
			return
		}
		sqsListener, ok := c.MustGet("sqsListener").(*sqs.Listener)
		if !ok {
			slog.Error("Failed to get sqsListener")
			return
		}

		defer func() {
			handler.handler.OnDisconnect(c, c.Request, messageType, station, sqsListener)
			_ = handler.conn.Close()
		}()

		handler.handle(c.Request.Context(), c.Request, messageType, station, sqsListener)
	}
}

func (h *WSHandler) handle(c context.Context, r *http.Request, messageType events.EventType, station string, sqsListener *sqs.Listener) {
	writer := wsWriter{
		writer: make(chan Message, bufferSize),
		error:  make(chan string),
	}
	h.handler.OnConnect(c, r, writer, messageType, station, sqsListener)

	go func() {
		for {
			t, msg, err := h.conn.ReadMessage()
			if err != nil {
				writer.Error("read failed")
				break
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
				h.handler.OnMessage(c, r, writer, msg, t)
			}
		}
	}()

	for {
		select {
		case <-c.Done():
			return
		case <-writer.error:
			return
		case msg := <-writer.writer:
			err := h.conn.WriteMessage(msg.Type, msg.Data)
			if err != nil {
				return
			}
		}
	}
}
