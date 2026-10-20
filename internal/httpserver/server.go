package httpserver

import (
	"time"

	"paypal/server/internal/config"
	"paypal/server/internal/router"

	"github.com/cloudwego/hertz/pkg/app/server"
)

func New(c config.Config) *server.Hertz {
	h := server.Default(
		server.WithHostPorts(c.HTTP.Address),
		server.WithReadTimeout(time.Duration(c.HTTP.ReadTimeoutSeconds)*time.Second),
		server.WithWriteTimeout(time.Duration(c.HTTP.WriteTimeoutSeconds)*time.Second),
		server.WithExitWaitTime(time.Duration(c.HTTP.ShutdownTimeoutSeconds)*time.Second),
	)
	router.Register(h)
	return h
}
