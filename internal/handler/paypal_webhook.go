package handler

import (
	"context"
	"log"

	"github.com/cloudwego/hertz/pkg/app"
)

// PayPalWebhook handles POST /api/webhooks/paypal for inspecting incoming events.
// The context argument is unused; ctx provides the request body and response.
// It prints the raw body to the server log and acknowledges with an empty HTTP 200.
// This diagnostic handler does not verify signatures or process or persist events.
func PayPalWebhook(_ context.Context, ctx *app.RequestContext) {
	log.Printf("PayPal webhook received: %s", ctx.Request.Body())
	ctx.Status(200)
}
