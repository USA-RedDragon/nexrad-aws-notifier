package server

import (
	"net/http"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/config"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/events"
	websocketControllers "github.com/USA-RedDragon/nexrad-aws-notifier/internal/server/websocket"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/websocket"
	"github.com/gin-gonic/gin"
)

func applyRoutes(r *gin.Engine, config *config.HTTP, eventsChannel chan events.Event) {
	r.GET("/health", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	// One hub broadcasts to every connection; CreateHandler builds the
	// per-connection state itself.
	hub := websocketControllers.NewEventsHub(eventsChannel)

	ws := r.Group("/ws")
	ws.GET("/events/:type/:station", websocket.CreateHandler(hub.NewConnection, config))
}
