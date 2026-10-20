package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
)

func Ping(_ context.Context, ctx *app.RequestContext) {
	ctx.String(200, "pong")
}
