package httpserver

import (
	"testing"

	"paypal/server/internal/config"

	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestNewServerRoutes(t *testing.T) {
	c, err := config.Load("../../configs/local.json")
	if err != nil {
		t.Fatal(err)
	}
	h := New(c)
	for _, path := range []string{"/ping", "/healthz"} {
		r := ut.PerformRequest(h.Engine, "GET", path, nil)
		if r.Code != 200 {
			t.Fatalf("%s returned %d", path, r.Code)
		}
	}
}
