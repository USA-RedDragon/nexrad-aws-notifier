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

	ws := r.Group("/ws")
	ws.GET("/events/:type/:station", websocket.CreateHandler(websocketControllers.CreateEventsWebsocket(eventsChannel), config))
}
