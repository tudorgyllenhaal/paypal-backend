package handler

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
)

func TestPayPalWebhookLogsRawBody(t *testing.T) {
	previous := log.Writer()
	var output bytes.Buffer
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	for _, body := range []string{`{"id":"TEST-EVENT","event_type":"PAYMENT.CAPTURE.COMPLETED"}`, "not JSON", ""} {
		t.Run(body, func(t *testing.T) {
			output.Reset()
			ctx := app.NewContext(0)
			ctx.Request.SetBodyString(body)
			PayPalWebhook(context.Background(), ctx)
			if !strings.Contains(output.String(), "PayPal webhook received: "+body) {
				t.Fatalf("body was not logged: %q", output.String())
			}
			if ctx.Response.StatusCode() != 200 || len(ctx.Response.Body()) != 0 {
				t.Fatal("expected an empty HTTP 200 acknowledgement")
			}
		})
	}
}
