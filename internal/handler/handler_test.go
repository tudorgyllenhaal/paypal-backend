package handler

import (
	"context"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
)

func TestPing(t *testing.T) {
	ctx := app.NewContext(0)
	Ping(context.Background(), ctx)
	if ctx.Response.StatusCode() != 200 || string(ctx.Response.Body()) != "pong" {
		t.Fatal("unexpected ping response")
	}
}
func TestHealth(t *testing.T) {
	ctx := app.NewContext(0)
	Health(context.Background(), ctx)
	if ctx.Response.StatusCode() != 200 || string(ctx.Response.Body()) != `{"status":"ok"}` {
		t.Fatal("unexpected health response")
	}
}
