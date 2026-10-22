package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type credentials struct {
	Environment  string `json:"environment"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// Read the same credential file as paypal-check, without requiring history dates.
func loadCredentials(path string) (credentials, error) {
	var c credentials
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

type client struct {
	http        *http.Client
	token       string
	credentials credentials
}

func newClient(c credentials) *client {
	return &client{credentials: c, http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *client) post(ctx context.Context, path, requestID string, body []byte, dst any) error {
	// API host is fixed; configuration cannot redirect payment calls to live.
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api-m.sandbox.paypal.com"+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cannot construct sandbox request")
	}
	req.Header.Set("Accept", "application/json")
	if path == "/v1/oauth2/token" {
		req.SetBasicAuth(c.credentials.ClientID, c.credentials.ClientSecret)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("PayPal-Request-Id", requestID)
		req.Header.Set("Prefer", "return=representation")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sandbox request failed; for payment operations retry only with the same request-id and arguments")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		switch resp.StatusCode {
		case 401, 403:
			return fmt.Errorf("sandbox authorization failed; check the seller app credentials and permissions")
		case 422:
			return fmt.Errorf("sandbox rejected the operation; ensure the buyer approved the order, funds and currency are valid, and the order has not already been captured")
		case 429:
			return fmt.Errorf("sandbox rate limit reached; retry later with the same request-id and arguments")
		default:
			return fmt.Errorf("sandbox returned HTTP %d; retry payment operations only with the same request-id and arguments", resp.StatusCode)
		}
	}
	d := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("sandbox returned invalid JSON; retain the request-id before retrying")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("sandbox returned trailing or oversized JSON; retain the request-id before retrying")
	}
	return nil
}
func (c *client) authenticate(ctx context.Context) error {
	var token struct {
		Value string `json:"access_token"`
		Type  string `json:"token_type"`
	}
	if err := c.post(ctx, "/v1/oauth2/token", "", []byte("grant_type=client_credentials"), &token); err != nil {
		return err
	}
	if token.Value == "" || !strings.EqualFold(token.Type, "Bearer") {
		return fmt.Errorf("sandbox returned an invalid token response")
	}
	c.token = token.Value
	return nil
}

type Capture struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
type Result struct {
	OrderID     string    `json:"order_id"`
	Status      string    `json:"status"`
	ApprovalURL string    `json:"approval_url,omitempty"`
	Captures    []Capture `json:"captures,omitempty"`
}
type order struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Links  []struct {
		Rel  string `json:"rel"`
		Href string `json:"href"`
	} `json:"links"`
	Units []struct {
		Payments struct {
			Captures []Capture `json:"captures"`
		} `json:"payments"`
	} `json:"purchase_units"`
}

func (c *client) create(ctx context.Context, amount, currency, returnURL, cancelURL, requestID string) (Result, error) {
	body := map[string]any{"intent": "CAPTURE", "purchase_units": []any{map[string]any{"amount": map[string]string{"currency_code": currency, "value": amount}}}, "payment_source": map[string]any{"paypal": map[string]any{"experience_context": map[string]string{"return_url": returnURL, "cancel_url": cancelURL, "user_action": "PAY_NOW", "shipping_preference": "NO_SHIPPING"}}}}
	data, _ := json.Marshal(body)
	var response order
	if err := c.post(ctx, "/v2/checkout/orders", requestID, data, &response); err != nil {
		return Result{}, err
	}
	if !idPattern.MatchString(response.ID) || response.Status == "" {
		return Result{}, fmt.Errorf("sandbox returned an invalid order response")
	}
	for _, link := range response.Links {
		if link.Rel != "approve" && link.Rel != "payer-action" {
			continue
		}
		u, err := url.Parse(link.Href)
		if err != nil || u.Scheme != "https" || u.User != nil || (u.Host != "www.sandbox.paypal.com" && u.Host != "sandbox.paypal.com") {
			continue
		}
		return Result{OrderID: response.ID, Status: response.Status, ApprovalURL: link.Href}, nil
	}
	return Result{}, fmt.Errorf("sandbox returned no valid buyer approval URL; retain the request-id before retrying")
}
func (c *client) capture(ctx context.Context, orderID, requestID string) (Result, error) {
	var response order
	if err := c.post(ctx, "/v2/checkout/orders/"+orderID+"/capture", requestID, []byte("{}"), &response); err != nil {
		return Result{}, err
	}
	if response.ID != orderID || response.Status == "" {
		return Result{}, fmt.Errorf("sandbox returned an invalid capture order response")
	}
	result := Result{OrderID: response.ID, Status: response.Status}
	for _, unit := range response.Units {
		for _, capture := range unit.Payments.Captures {
			if !idPattern.MatchString(capture.ID) || capture.Status == "" {
				return Result{}, fmt.Errorf("sandbox returned invalid capture details")
			}
			result.Captures = append(result.Captures, capture)
		}
	}
	if len(result.Captures) == 0 {
		return Result{}, fmt.Errorf("sandbox returned no capture details; retain the request-id before retrying")
	}
	return result, nil
}
func validCallback(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	return u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))
}
