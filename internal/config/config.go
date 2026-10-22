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

type MySQL struct {
	Host                   string `json:"host"`
	Port                   int    `json:"port"`
	Database               string `json:"database"`
	Username               string `json:"username"`
	Password               string `json:"password"`
	TLS                    bool   `json:"tls"`
	TLSCAFile              string `json:"tls_ca_file"`
	ConnectTimeoutSeconds  int    `json:"connect_timeout_seconds"`
	IOTimeoutSeconds       int    `json:"io_timeout_seconds"`
	StartupTimeoutSeconds  int    `json:"startup_timeout_seconds"`
	MaxOpenConns           int    `json:"max_open_conns"`
	MaxIdleConns           int    `json:"max_idle_conns"`
	ConnMaxLifetimeSeconds int    `json:"conn_max_lifetime_seconds"`
}

type Config struct {
	Mode  string `json:"mode"`
	MySQL MySQL  `json:"mysql"`
	HTTP  struct {
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
	if err := c.MySQL.applyEnvironment(); err != nil {
		return c, err
	}
	if err := c.MySQL.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

// ECS injects deployment settings through these variables; local JSON remains sufficient.
func (c *MySQL) applyEnvironment() error {
	for name, dst := range map[string]*string{
		"MYSQL_HOST": &c.Host, "MYSQL_DATABASE": &c.Database,
		"MYSQL_USER": &c.Username, "MYSQL_PASSWORD": &c.Password,
		"MYSQL_TLS_CA_FILE": &c.TLSCAFile,
	} {
		if value, ok := os.LookupEnv(name); ok {
			*dst = value
		}
	}
	if value, ok := os.LookupEnv("MYSQL_PORT"); ok {
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("MYSQL_PORT must be an integer")
		}
		c.Port = n
	}
	if value, ok := os.LookupEnv("MYSQL_TLS"); ok {
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("MYSQL_TLS must be a boolean")
		}
		c.TLS = b
	}
	return nil
}

func (c MySQL) Validate() error {
	if c.Host == "" || c.Database == "" || c.Username == "" || c.Password == "" {
		return fmt.Errorf("mysql host, database, username and password are required (JSON or MYSQL_* environment variables)")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("mysql port must be between 1 and 65535")
	}
	if c.MaxOpenConns <= 0 || c.MaxIdleConns < 0 || c.MaxIdleConns > c.MaxOpenConns {
		return fmt.Errorf("mysql pool requires max_open_conns > 0 and 0 <= max_idle_conns <= max_open_conns")
	}
	for _, seconds := range []int{c.ConnectTimeoutSeconds, c.IOTimeoutSeconds, c.StartupTimeoutSeconds, c.ConnMaxLifetimeSeconds} {
		if seconds <= 0 || int64(seconds) > int64((1<<63-1)/time.Second) {
			return fmt.Errorf("mysql timeouts and connection lifetime must be positive durations")
		}
	}
	if !c.TLS && c.TLSCAFile != "" {
		return fmt.Errorf("mysql tls_ca_file requires tls=true")
	}
	return nil
}
