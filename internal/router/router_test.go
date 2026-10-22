package router

import (
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
