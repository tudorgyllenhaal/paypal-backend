package config

import (
	"os"
	"strings"
	"testing"
)

func localConfig(t *testing.T) Config {
	t.Helper()
	for _, key := range []string{"MYSQL_HOST", "MYSQL_PORT", "MYSQL_DATABASE", "MYSQL_USER", "MYSQL_PASSWORD", "MYSQL_TLS", "MYSQL_TLS_CA_FILE"} {
		previous, present := os.LookupEnv(key)
		os.Unsetenv(key)
		t.Cleanup(func() {
			if present {
				os.Setenv(key, previous)
			} else {
				os.Unsetenv(key)
			}
		})
	}
	c, err := Load("../../configs/local.json")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLocalMySQLConfig(t *testing.T) {
	c := localConfig(t)
	if c.MySQL.Host != "127.0.0.1" || c.MySQL.Port != 3306 || c.MySQL.TLS {
		t.Fatalf("unexpected local connection settings")
	}
}

func TestAWSMySQLOverrides(t *testing.T) {
	localConfig(t)
	for key, value := range map[string]string{
		"MYSQL_HOST": "database.example.com", "MYSQL_PORT": "3307",
		"MYSQL_DATABASE": "payments", "MYSQL_USER": "app", "MYSQL_PASSWORD": "test-secret",
		"MYSQL_TLS": "true", "MYSQL_TLS_CA_FILE": "/certs/mysql.pem",
	} {
		t.Setenv(key, value)
	}
	c, err := Load("../../configs/aws.json")
	if err != nil {
		t.Fatal(err)
	}
	if c.MySQL.Host != "database.example.com" || c.MySQL.Port != 3307 || c.MySQL.Database != "payments" || c.MySQL.Username != "app" || c.MySQL.Password != "test-secret" || !c.MySQL.TLS || c.MySQL.TLSCAFile != "/certs/mysql.pem" {
		t.Fatal("ECS overrides were not applied")
	}
}

func TestMySQLConfigRejectsInvalidSettings(t *testing.T) {
	for _, tc := range []struct{ name, key, value, want string }{
		{"missing password", "MYSQL_PASSWORD", "", "required"},
		{"non-numeric port", "MYSQL_PORT", "bad", "integer"},
		{"invalid port", "MYSQL_PORT", "65536", "between"},
		{"invalid TLS", "MYSQL_TLS", "bad", "boolean"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			localConfig(t)
			t.Setenv(tc.key, tc.value)
			_, err := Load("../../configs/local.json")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
	c := localConfig(t)
	c.MySQL.MaxIdleConns = c.MySQL.MaxOpenConns + 1
	if err := c.MySQL.Validate(); err == nil {
		t.Fatal("invalid pool accepted")
	}
	c = localConfig(t)
	c.MySQL.StartupTimeoutSeconds = 0
	if err := c.MySQL.Validate(); err == nil {
		t.Fatal("invalid timeout accepted")
	}
}
