package database

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"paypal/server/internal/config"

	"github.com/go-sql-driver/mysql"
)

// OpenMySQL initializes a reusable connection pool and verifies database access.
func OpenMySQL(ctx context.Context, c config.MySQL) (*sql.DB, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	driverConfig := mysql.NewConfig()
	driverConfig.Net = "tcp"
	driverConfig.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	driverConfig.User = c.Username
	driverConfig.Passwd = c.Password
	driverConfig.DBName = c.Database
	driverConfig.ParseTime = true
	driverConfig.Timeout = time.Duration(c.ConnectTimeoutSeconds) * time.Second
	driverConfig.ReadTimeout = time.Duration(c.IOTimeoutSeconds) * time.Second
	driverConfig.WriteTimeout = time.Duration(c.IOTimeoutSeconds) * time.Second
	if c.TLS {
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("load MySQL system CAs: %w", err)
		}
		if c.TLSCAFile != "" {
			pem, err := os.ReadFile(c.TLSCAFile)
			if err != nil {
				return nil, fmt.Errorf("read MySQL CA file: %w", err)
			}
			if !roots.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("MySQL CA file contains no certificates")
			}
		}
		driverConfig.TLS = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: c.Host}
	}
	connector, err := mysql.NewConnector(driverConfig)
	if err != nil {
		return nil, fmt.Errorf("configure MySQL connector: %w", err)
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(c.MaxOpenConns)
	db.SetMaxIdleConns(c.MaxIdleConns)
	db.SetConnMaxLifetime(time.Duration(c.ConnMaxLifetimeSeconds) * time.Second)
	pingCtx, cancel := context.WithTimeout(ctx, time.Duration(c.StartupTimeoutSeconds)*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to MySQL: %w", err)
	}
	return db, nil
}
