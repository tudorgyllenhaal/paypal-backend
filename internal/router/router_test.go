package router

import (
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestRegisteredRoutes(t *testing.T) {
	h := server.New()
	Register(h)
	for _, tc := range []struct {
		path, body string
		status     int
	}{{"/ping", "pong", 200}, {"/healthz", `{"status":"ok"}`, 200}, {"/unknown", "", 404}} {
		r := ut.PerformRequest(h.Engine, "GET", tc.path, nil)
		if r.Code != tc.status || (tc.body != "" && r.Body.String() != tc.body) {
			t.Fatalf("%s: %d %s", tc.path, r.Code, r.Body.String())
		}
	}
	if r := ut.PerformRequest(h.Engine, "POST", "/ping", nil); r.Code == 200 {
		t.Fatal("wrong method accepted")
	}
}

func TestPayPalWebhookRoute(t *testing.T) {
	h := server.New()
	Register(h)
	payload := `{"id":"TEST-WEBHOOK"}`
	response := ut.PerformRequest(h.Engine, "POST", "/api/webhooks/paypal", &ut.Body{Body: strings.NewReader(payload), Len: len(payload)})
	if response.Code != 200 || response.Body.Len() != 0 {
		t.Fatalf("unexpected webhook acknowledgement: %d %q", response.Code, response.Body.String())
	}
	if r := ut.PerformRequest(h.Engine, "GET", "/api/webhooks/paypal", nil); r.Code == 200 {
		t.Fatal("webhook accepted GET")
	}
}
