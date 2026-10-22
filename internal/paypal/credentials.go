package paypal

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type Credentials struct {
	Environment  string `json:"environment"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// Read the same credential file as paypal-check, without requiring history dates.
func LoadSandboxCredentials(path string) (Credentials, error) {
	var c Credentials
	f, err := os.Open(path)
	if err != nil {
		return c, fmt.Errorf("cannot open PayPal configuration")
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	if err := d.Decode(&c); err != nil {
		return c, fmt.Errorf("invalid PayPal configuration JSON")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return c, fmt.Errorf("PayPal configuration must contain one JSON object")
	}
	if value, ok := os.LookupEnv("PAYPAL_CLIENT_ID"); ok {
		c.ClientID = value
	}
	if value, ok := os.LookupEnv("PAYPAL_CLIENT_SECRET"); ok {
		c.ClientSecret = value
	}
	if c.Environment != "sandbox" {
		return c, fmt.Errorf("paypal-payment only supports sandbox; live execution is rejected")
	}
	if c.ClientID == "" || c.ClientSecret == "" {
		return c, fmt.Errorf("sandbox seller client ID and secret are required")
	}
	return c, nil
}
