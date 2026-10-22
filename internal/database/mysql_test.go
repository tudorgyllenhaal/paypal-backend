package database

import (
	"context"
	"os"
	"strings"
	"testing"

	"paypal/server/internal/config"
)

func TestMissingTLSCAFile(t *testing.T) {
	c, err := config.Load("../../configs/local.json")
	if err != nil {
		t.Fatal(err)
	}
	c.MySQL.TLS = true
	c.MySQL.TLSCAFile = "nonexistent-mysql-ca.pem"
	db, err := OpenMySQL(context.Background(), c.MySQL)
	if err == nil || db != nil || !strings.Contains(err.Error(), "read MySQL CA file") {
		t.Fatalf("expected missing CA rejection, got %v", err)
	}
}

func TestMySQLIntegration(t *testing.T) {
	if os.Getenv("MYSQL_INTEGRATION_TEST") != "1" {
		t.Skip("set MYSQL_INTEGRATION_TEST=1 with local MySQL running")
	}
	c, err := config.Load("../../configs/local.json")
	if err != nil {
		t.Fatal(err)
	}
	db, err := OpenMySQL(context.Background(), c.MySQL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name string
	var one int
	if err := db.QueryRowContext(context.Background(), "SELECT DATABASE(), 1").Scan(&name, &one); err != nil {
		t.Fatal(err)
	}
	if name != c.MySQL.Database || one != 1 {
		t.Fatal("unexpected database response")
	}
	if db.Stats().MaxOpenConnections != c.MySQL.MaxOpenConns {
		t.Fatal("pool limit not applied")
	}
	c.MySQL.Password += "-incorrect"
	failedDB, err := OpenMySQL(context.Background(), c.MySQL)
	if failedDB != nil {
		failedDB.Close()
		t.Fatal("authentication failure returned an open pool")
	}
	if err == nil {
		t.Fatal("incorrect credentials accepted")
	}
	if strings.Contains(err.Error(), c.MySQL.Password) {
		t.Fatal("password exposed in error")
	}
}
