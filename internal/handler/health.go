package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
)

func Health(_ context.Context, ctx *app.RequestContext) {
	ctx.JSON(200, utils.H{"status": "ok"})
}
