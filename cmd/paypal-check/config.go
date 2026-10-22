package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"
)

type Config struct {
	Environment    string   `json:"environment"`
	ClientID       string   `json:"client_id"`
	ClientSecret   string   `json:"client_secret"`
	LookbackHours  int      `json:"lookback_hours,omitempty"`
	StartAt        string   `json:"start_at"`
	EndAt          string   `json:"end_at"`
	MaxPages       int      `json:"max_pages"`
	WebhookURL     string   `json:"webhook_url"`
	ExpectedEvents []string `json:"expected_webhook_events"`
	ExpectedCodes  []string `json:"expected_transaction_event_codes"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	if err := readJSON(path, &c, true); err != nil {
		return c, err
	}
	for key, target := range map[string]*string{"PAYPAL_CLIENT_ID": &c.ClientID, "PAYPAL_CLIENT_SECRET": &c.ClientSecret} {
		if value, ok := os.LookupEnv(key); ok {
			*target = value
		}
	}
	return c, c.Validate()
}

// resolveRange freezes a rolling UTC window for all pages in one run.
func (c Config) resolveRange(now time.Time) (Config, error) {
	if c.LookbackHours < 0 || c.LookbackHours > 31*24 {
		return c, fmt.Errorf("lookback_hours must be between 0 and 744")
	}
	if c.LookbackHours > 0 || (c.StartAt == "" && c.EndAt == "") {
		hours := c.LookbackHours
		if hours == 0 {
			hours = 24
		}
		end := now.UTC().Truncate(time.Second)
		c.StartAt = end.Add(-time.Duration(hours) * time.Hour).Format(time.RFC3339)
		c.EndAt = end.Format(time.RFC3339)
	}
	return c, nil
}

func (c Config) Validate() error {
	var err error
	c, err = c.resolveRange(time.Now())
	if err != nil {
		return err
	}
	if c.Environment != "sandbox" && c.Environment != "live" {
		return fmt.Errorf("environment must be sandbox or live")
	}
	if c.ClientID == "" || c.ClientSecret == "" {
		return fmt.Errorf("PayPal client ID and secret are required in private config or PAYPAL_CLIENT_ID/PAYPAL_CLIENT_SECRET")
	}
	start, err := time.Parse(time.RFC3339, c.StartAt)
	if err != nil {
		return fmt.Errorf("start_at must be RFC3339")
	}
	end, err := time.Parse(time.RFC3339, c.EndAt)
	if err != nil {
		return fmt.Errorf("end_at must be RFC3339")
	}
	if !end.After(start) || end.Sub(start) > 31*24*time.Hour {
		return fmt.Errorf("date range must be positive and at most 31 days")
	}
	if c.MaxPages < 1 || c.MaxPages > 1000 {
		return fmt.Errorf("max_pages must be between 1 and 1000")
	}
	if c.WebhookURL != "" {
		u, err := url.Parse(c.WebhookURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return fmt.Errorf("webhook_url must be an HTTPS listener URL without credentials")
		}
	}
	for _, event := range c.ExpectedEvents {
		if !eventPattern.MatchString(event) {
			return fmt.Errorf("expected_webhook_events contains an invalid event name")
		}
	}
	for _, code := range c.ExpectedCodes {
		if !codePattern.MatchString(code) {
			return fmt.Errorf("expected_transaction_event_codes must contain T followed by four digits")
		}
	}
	return nil
}

func readJSON(path string, dst any, strict bool) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open JSON input file")
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 4<<20))
	if strict {
		d.DisallowUnknownFields()
	}
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON input file")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("JSON input must contain one object")
	}
	return nil
}

/* Temporarily disabled: identity-mapping sample validation.
func (s Samples) validate(environment string) error {
	if len(s.Captures)+len(s.Refunds)+len(s.Webhooks) == 0 {
		return nil
	}
	if s.Environment != environment {
		return fmt.Errorf("sample environment must match the configured PayPal environment")
	}
	return nil
}
*/
