package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Mode string `json:"mode"`
	HTTP struct {
		Address                string `json:"address"`
		ReadTimeoutSeconds     int    `json:"read_timeout_seconds"`
		WriteTimeoutSeconds    int    `json:"write_timeout_seconds"`
		ShutdownTimeoutSeconds int    `json:"shutdown_timeout_seconds"`
	} `json:"http"`
}

func Load(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, fmt.Errorf("decode config: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return c, fmt.Errorf("config must contain one JSON object")
	}
	if c.Mode != "local" && c.Mode != "aws" {
		return c, fmt.Errorf("mode must be local or aws")
	}
	_, port, err := net.SplitHostPort(c.HTTP.Address)
	if err != nil {
		return c, fmt.Errorf("invalid HTTP address: %w", err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return c, fmt.Errorf("invalid HTTP port")
	}
	for _, seconds := range []int{c.HTTP.ReadTimeoutSeconds, c.HTTP.WriteTimeoutSeconds, c.HTTP.ShutdownTimeoutSeconds} {
		if seconds <= 0 || int64(seconds) > int64((1<<63-1)/time.Second) {
			return c, fmt.Errorf("timeouts must be positive durations")
		}
	}
	return c, nil
}
