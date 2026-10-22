package router

import (
	"paypal/server/internal/handler"

	"github.com/cloudwego/hertz/pkg/app/server"
)

// Register adds the server API routes to h, the Hertz server being configured.
// Add new API routes here; handler functions implement request behavior.
func Register(h *server.Hertz) {
	h.GET("/healthz", handler.Health)
	h.GET("/ping", handler.Ping)
	h.POST("/api/webhooks/paypal", handler.PayPalWebhook)
}
