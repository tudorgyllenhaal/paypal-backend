package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"paypal/server/internal/paypal"
	"strings"
	"testing"
)

const configJSON = `{"environment":"sandbox","client_id":"seller-id","client_secret":"seller-secret","start_at":"ignored-by-payment-tool"}`
const tokenJSON = `{"access_token":"private-token","token_type":"Bearer"}`
const createJSON = `{"id":"ORDER123","status":"PAYER_ACTION_REQUIRED","links":[{"rel":"payer-action","href":"https://www.sandbox.paypal.com/checkoutnow?token=ORDER123"}]}`
const captureJSON = `{"id":"ORDER123","status":"COMPLETED","purchase_units":[{"payments":{"captures":[{"id":"CAPTURE123","status":"COMPLETED"}]}}]}`

func input(t *testing.T, body string) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", ".test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "input.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func mock(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	old := http.DefaultTransport
	target, _ := url.Parse(s.URL)
	http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api-m.sandbox.paypal.com" || r.URL.Scheme != "https" {
			t.Errorf("non-sandbox request: %s", r.URL)
		}
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = target.Scheme
		u.Host = target.Host
		clone.URL = &u
		return old.RoundTrip(clone)
	})
	t.Cleanup(func() { http.DefaultTransport = old })
}
func normal(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Method != "POST" {
		t.Error("expected POST")
	}
	if r.URL.Path == "/v1/oauth2/token" {
		id, secret, ok := r.BasicAuth()
		if !ok || id != "seller-id" || secret != "seller-secret" {
			t.Error("bad credentials")
		}
		r.ParseForm()
		if r.Form.Get("grant_type") != "client_credentials" {
			t.Error("bad OAuth body")
		}
		fmt.Fprint(w, tokenJSON)
		return
	}
	if r.Header.Get("Authorization") != "Bearer private-token" || r.Header.Get("PayPal-Request-Id") == "" || r.Header.Get("Prefer") != "return=representation" {
		t.Error("missing operation headers")
	}
	switch r.URL.Path {
	case "/v2/checkout/orders":
		var body struct {
			Intent string `json:"intent"`
			Units  []struct {
				Amount struct {
					Currency string `json:"currency_code"`
					Value    string `json:"value"`
				} `json:"amount"`
			} `json:"purchase_units"`
			Source struct {
				PayPal struct {
					Experience map[string]string `json:"experience_context"`
				} `json:"paypal"`
			} `json:"payment_source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Intent != "CAPTURE" || len(body.Units) != 1 || body.Units[0].Amount.Value != "1.25" || body.Units[0].Amount.Currency != "USD" || body.Source.PayPal.Experience["user_action"] != "PAY_NOW" || body.Source.PayPal.Experience["return_url"] != "http://localhost:8080/ping" {
			t.Error("wrong create payload")
		}
		w.WriteHeader(201)
		fmt.Fprint(w, createJSON)
	case "/v2/checkout/orders/ORDER123/capture":
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 0 {
			t.Error("bad capture body")
		}
		w.WriteHeader(201)
		fmt.Fprint(w, captureJSON)
	default:
		t.Errorf("unexpected path %s", r.URL.Path)
		w.WriteHeader(404)
	}
}
func arguments(t *testing.T, action string) []string {
	t.Helper()
	args := []string{action, "-config", input(t, configJSON), "-request-id", "test-" + action + "-001"}
	if action == "create" {
		return append(args, "-amount", "1.25")
	}
	return append(args, "-order-id", "ORDER123")
}
func TestCreateAndCapture(t *testing.T) {
	for _, action := range []string{"create", "capture"} {
		t.Run(action, func(t *testing.T) {
			mock(t, func(w http.ResponseWriter, r *http.Request) { normal(t, w, r) })
			var out, errs bytes.Buffer
			if code := run(context.Background(), arguments(t, action), &out, &errs); code != 0 {
				t.Fatal(code, errs.String())
			}
			var result paypal.PaymentResult
			if json.Unmarshal(out.Bytes(), &result) != nil || result.OrderID != "ORDER123" {
				t.Fatal(out.String())
			}
			if action == "create" && result.ApprovalURL == "" {
				t.Fatal("no approval URL")
			}
			if action == "capture" && (len(result.Captures) != 1 || result.Captures[0].ID != "CAPTURE123" || result.Captures[0].Status != "COMPLETED") {
				t.Fatal(result)
			}
			for _, secret := range []string{"seller-secret", "private-token"} {
				if strings.Contains(out.String()+errs.String(), secret) {
					t.Fatal("credential leak")
				}
			}
		})
	}
}
func TestNoRequestOnInvalidInput(t *testing.T) {
	requests := 0
	mock(t, func(http.ResponseWriter, *http.Request) { requests++ })
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no action", nil}, {"unknown", []string{"delete"}}, {"flags", []string{"create", "-unknown"}},
		{"positional", []string{"create", "extra"}},
		{"missing key", []string{"create", "-amount", "1.25"}},
		{"invalid key", []string{"create", "-request-id", "bad/key", "-amount", "1.25"}},
		{"invalid amount", []string{"create", "-request-id", "key", "-amount", "0"}},
		{"path injection", []string{"capture", "-request-id", "key", "-order-id", "../live"}},
		{"capture rejects create flags", []string{"capture", "-amount", "1.25"}},
		{"missing config", []string{"capture", "-request-id", "key", "-order-id", "ORDER123", "-config", "absent.json"}},
		{"live rejected", []string{"create", "-request-id", "key", "-amount", "1.25", "-config", input(t, `{"environment":"live","client_id":"seller-id","client_secret":"seller-secret"}`)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errs bytes.Buffer
			if run(context.Background(), tc.args, &out, &errs) != 1 || errs.Len() == 0 || out.Len() != 0 {
				t.Fatal("invalid input accepted")
			}
		})
	}
	if requests != 0 {
		t.Fatal("invalid input made a network request")
	}
}
func TestValidation(t *testing.T) {
	for _, amount := range []string{"", "0", "0.00", "-1", "1e2", "01", "1.001", "1000000000"} {
		if validatePayment(amount, "USD", "http://localhost", "https://example.com") == nil {
			t.Errorf("amount accepted: %s", amount)
		}
	}
	for _, currency := range []string{"usd", "US", "USDD"} {
		if validatePayment("1", currency, "http://localhost", "https://example.com") == nil {
			t.Error("invalid currency")
		}
	}
	for _, currency := range []string{"JPY", "HUF", "TWD"} {
		if validatePayment("1.25", currency, "http://localhost", "https://example.com") == nil {
			t.Error("invalid fractional amount")
		}
		if validatePayment("1", currency, "http://localhost", "https://example.com") != nil {
			t.Error("integer currency rejected")
		}
	}
	for _, callback := range []string{"http://external.example", "https://user:pass@example.com", "https://example.com/#secret", ":bad", "/relative"} {
		if validatePayment("1.25", "USD", callback, "http://localhost") == nil {
			t.Error("invalid callback accepted")
		}
	}
	for _, callback := range []string{"https://example.com/return", "http://localhost:8080/ping", "http://127.0.0.1:8080/ping", "http://[::1]:8080/ping"} {
		if !validCallback(callback) {
			t.Errorf("valid callback rejected: %s", callback)
		}
	}
}
func TestCredentials(t *testing.T) {
	for _, raw := range []string{"{", `{} {}`, `{"environment":"sandbox"}`, `{"environment":"live","client_id":"id","client_secret":"secret"}`} {
		if _, err := paypal.LoadSandboxCredentials(input(t, raw)); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	t.Setenv("PAYPAL_CLIENT_ID", "env-id")
	t.Setenv("PAYPAL_CLIENT_SECRET", "env-secret")
	c, err := paypal.LoadSandboxCredentials(input(t, configJSON))
	if err != nil || c.ClientID != "env-id" || c.ClientSecret != "env-secret" {
		t.Fatal("environment override failed")
	}
	t.Setenv("PAYPAL_CLIENT_SECRET", "")
	if _, err := paypal.LoadSandboxCredentials(input(t, configJSON)); err == nil {
		t.Fatal("empty secret accepted")
	}
}
func TestAPIErrors(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		status           int
	}{
		{"bad credentials", "/v1/oauth2/token", `{"secret":"seller-secret"}`, 401},
		{"forbidden", "/v2/checkout/orders", "", 403},
		{"not approved", "/v2/checkout/orders/ORDER123/capture", "", 422},
		{"rate limited", "/v2/checkout/orders", "", 429},
		{"server error", "/v2/checkout/orders", "", 500},
		{"malformed", "/v2/checkout/orders", "{", 201},
		{"trailing JSON", "/v2/checkout/orders", createJSON + ` {}`, 201},
		{"missing token", "/v1/oauth2/token", `{"token_type":"Bearer"}`, 200},
		{"bad order", "/v2/checkout/orders", `{"status":"CREATED"}`, 201},
		{"no approval URL", "/v2/checkout/orders", `{"id":"ORDER123","status":"CREATED","links":[{"rel":"self","href":"https://api-m.sandbox.paypal.com"}]}`, 201},
		{"live approval URL", "/v2/checkout/orders", `{"id":"ORDER123","status":"CREATED","links":[{"rel":"approve","href":"https://www.paypal.com/checkoutnow"}]}`, 201},
		{"wrong order ID", "/v2/checkout/orders/ORDER123/capture", `{"id":"OTHER","status":"COMPLETED"}`, 201},
		{"missing capture", "/v2/checkout/orders/ORDER123/capture", `{"id":"ORDER123","status":"COMPLETED"}`, 201},
		{"invalid capture", "/v2/checkout/orders/ORDER123/capture", `{"id":"ORDER123","status":"COMPLETED","purchase_units":[{"payments":{"captures":[{"status":"COMPLETED"}]}}]}`, 201},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			mock(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tc.path {
					calls++
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.body)
					return
				}
				normal(t, w, r)
			})
			action := "create"
			if strings.HasSuffix(tc.path, "/capture") {
				action = "capture"
			}
			var out, errs bytes.Buffer
			if run(context.Background(), arguments(t, action), &out, &errs) != 1 || errs.Len() == 0 || out.Len() != 0 || calls != 1 {
				t.Fatal("API error not reported or retried unsafely")
			}
			if strings.Contains(errs.String(), "seller-secret") {
				t.Fatal("response body leaked")
			}
		})
	}
}
func TestRedirectNotFollowed(t *testing.T) {
	calls := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer target.Close()
	mock(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	var out, errs bytes.Buffer
	if run(context.Background(), arguments(t, "create"), &out, &errs) != 1 || calls != 0 {
		t.Fatal("redirect followed")
	}
}
func TestNetworkFailure(t *testing.T) {
	old := http.DefaultTransport
	http.DefaultTransport = transportFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("seller-secret") })
	defer func() { http.DefaultTransport = old }()
	var out, errs bytes.Buffer
	if run(context.Background(), arguments(t, "create"), &out, &errs) != 1 || strings.Contains(errs.String(), "seller-secret") {
		t.Fatal("unsafe network error")
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken output") }
func TestOutputFailureAndRetryKey(t *testing.T) {
	keys := []string{}
	mock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/checkout/orders" {
			keys = append(keys, r.Header.Get("PayPal-Request-Id"))
		}
		normal(t, w, r)
	})
	args := arguments(t, "create")
	var errs bytes.Buffer
	if run(context.Background(), args, brokenWriter{}, &errs) != 1 {
		t.Fatal("write failure ignored")
	}
	var out bytes.Buffer
	if run(context.Background(), args, &out, &errs) != 0 {
		t.Fatal(errs.String())
	}
	if len(keys) != 2 || keys[0] != keys[1] {
		t.Fatal("retry changed idempotency key")
	}
}
