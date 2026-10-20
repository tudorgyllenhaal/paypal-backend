package router

import (
	"paypal/server/internal/handler"

	"github.com/cloudwego/hertz/pkg/app/server"
)

// Register connects HTTP routes to their handlers. Add new API routes here.
func Register(h *server.Hertz) {
	h.GET("/healthz", handler.Health)
	h.GET("/ping", handler.Ping)
}
