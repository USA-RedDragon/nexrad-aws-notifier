package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/config"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/events"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/sqs"
	"github.com/gin-gonic/gin"
)

const clientIP = "192.0.2.1"

type nopWebsocket struct{}

func (nopWebsocket) OnMessage(context.Context, *http.Request, Writer, []byte, int) {}
func (nopWebsocket) OnConnect(context.Context, *http.Request, Writer, events.EventType, string, *sqs.Listener) error {
	return nil
}
func (nopWebsocket) OnDisconnect(context.Context, *http.Request, events.EventType, string, *sqs.Listener) {
}

func serve(t *testing.T, limiter *clientLimiter, path string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/ws/events/:type/:station", createHandler(func() Websocket { return nopWebsocket{} }, &config.HTTP{}, limiter))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	req.RemoteAddr = clientIP + ":1234"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// Anything in the path used to be subscribed to, and so written into the SNS
// filter policies.
func TestHandlerRejectsUnknownStations(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"/ws/events/nexrad-chunk/nonsense",
		"/ws/events/nexrad-archive/KZZZ",
		"/ws/events/nexrad-chunk/K%2A",
	} {
		rec := serve(t, newClientLimiter(maxSubscriptionsPerClient), path)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want %d", path, rec.Code, http.StatusBadRequest)
		}
		if !strings.Contains(rec.Body.String(), "unknown NEXRAD station") {
			t.Errorf("%s: body %q does not say why", path, rec.Body.String())
		}
	}
}

func TestHandlerRejectsUnknownTypes(t *testing.T) {
	t.Parallel()
	rec := serve(t, newClientLimiter(maxSubscriptionsPerClient), "/ws/events/nexrad-level3/KTLX")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "unknown event type") {
		t.Errorf("body %q does not say why", rec.Body.String())
	}
}

func TestHandlerRefusesAClientOverTheCap(t *testing.T) {
	t.Parallel()
	limiter := newClientLimiter(maxSubscriptionsPerClient)
	for range maxSubscriptionsPerClient {
		if !limiter.acquire(clientIP) {
			t.Fatal("acquire under the cap failed")
		}
	}
	rec := serve(t, limiter, "/ws/events/nexrad-chunk/KTLX")
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	if !strings.Contains(rec.Body.String(), "subscriptions") {
		t.Errorf("body %q does not say why", rec.Body.String())
	}
	// A refused request must not hold a slot.
	limiter.release(clientIP)
	if !limiter.acquire(clientIP) {
		t.Error("releasing one slot did not free it")
	}
}

func TestClientLimiter(t *testing.T) {
	t.Parallel()
	limiter := newClientLimiter(2)
	for range 2 {
		if !limiter.acquire("a") {
			t.Fatal("acquire under a cap of two failed")
		}
	}
	if limiter.acquire("a") {
		t.Fatal("third acquire over a cap of two succeeded")
	}
	if !limiter.acquire("b") {
		t.Fatal("one client's subscriptions counted against another")
	}
	limiter.release("a")
	if !limiter.acquire("a") {
		t.Fatal("release did not free a slot")
	}
	limiter.release("a")
	limiter.release("a")
	limiter.release("b")
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if len(limiter.counts) != 0 {
		t.Errorf("clients with nothing open are still tracked: %v", limiter.counts)
	}
}
