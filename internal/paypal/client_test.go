package paypal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	c := NewClient(Credentials{Environment: "sandbox", ClientID: "id", ClientSecret: "secret"}, s.Client())
	c.baseURL = s.URL
	return c
}

const tokenResponse = `{"access_token":"token","token_type":"Bearer"}`
const historyResponse = `{"transaction_details":[{"transaction_info":{"transaction_id":"CAPTURE1","transaction_event_code":"T0006"}}],"page":1,"total_pages":1}`
const orderResponse = `{"id":"ORDER1","status":"CREATED","links":[{"rel":"approve","href":"https://www.sandbox.paypal.com/checkoutnow?token=ORDER1"}]}`
const captureResponse = `{"id":"ORDER1","status":"COMPLETED","purchase_units":[{"payments":{"captures":[{"id":"CAPTURE1","status":"COMPLETED"}]}}]}`

var testQuery = Query{StartAt: "2026-10-01T00:00:00Z", EndAt: "2026-10-02T00:00:00Z", MaxPages: 3}

func normalAPI(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.URL.Path == "/v1/oauth2/token" {
		id, secret, ok := r.BasicAuth()
		if !ok || id != "id" || secret != "secret" {
			t.Error("bad credentials")
		}
		r.ParseForm()
		if r.Method != "POST" || r.Form.Get("grant_type") != "client_credentials" {
			t.Error("bad auth request")
		}
		fmt.Fprint(w, tokenResponse)
		return
	}
	if r.Header.Get("Authorization") != "Bearer token" {
		t.Error("missing bearer token")
	}
	if r.Method == "POST" {
		if r.Header.Get("PayPal-Request-Id") != "key" || r.Header.Get("Prefer") != "return=representation" {
			t.Error("missing mutation headers")
		}
	}
	switch r.URL.Path {
	case "/v1/reporting/transactions":
		if r.URL.Query().Get("start_date") != testQuery.StartAt || r.URL.Query().Get("page_size") != "500" {
			t.Error("bad query")
		}
		fmt.Fprint(w, historyResponse)
	case "/v1/notifications/webhooks":
		fmt.Fprint(w, `{"webhooks":[{"url":"https://example.com","event_types":[{"name":"PAYMENT.CAPTURE.COMPLETED"}]}]}`)
	case "/v2/checkout/orders":
		w.WriteHeader(201)
		fmt.Fprint(w, orderResponse)
	case "/v2/checkout/orders/ORDER1/capture":
		w.WriteHeader(201)
		fmt.Fprint(w, captureResponse)
	default:
		t.Error("unexpected endpoint")
		w.WriteHeader(404)
	}
}
func TestClientAPIs(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { normalAPI(t, w, r) })
	if err := c.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	records, complete, err := c.Transactions(context.Background(), testQuery)
	if err != nil || !complete || len(records) != 1 || records[0].Info.ID != "CAPTURE1" {
		t.Fatal(records, complete, err)
	}
	hooks, err := c.Webhooks(context.Background())
	if err != nil || len(hooks) != 1 {
		t.Fatal(hooks, err)
	}
	order, err := c.CreateOrder(context.Background(), "1.00", "USD", "http://localhost", "http://localhost", "key")
	if err != nil || order.OrderID != "ORDER1" || order.ApprovalURL == "" {
		t.Fatal(order, err)
	}
	capture, err := c.CaptureOrder(context.Background(), "ORDER1", "key")
	if err != nil || len(capture.Captures) != 1 || capture.Captures[0].ID != "CAPTURE1" {
		t.Fatal(capture, err)
	}
	if NewClient(Credentials{Environment: "live"}, nil).baseURL != "https://api-m.paypal.com" {
		t.Fatal("wrong live endpoint")
	}
}
func TestAPIResponseErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"401", "private-secret", 401}, {"403", "private-secret", 403}, {"422", "private-secret", 422}, {"429", "private-secret", 429}, {"500", "private-secret", 500},
		{"malformed", "{", 200}, {"trailing", tokenResponse + ` {}`, 200}, {"missing token", `{}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) })
			err := c.Authenticate(context.Background())
			if err == nil || strings.Contains(err.Error(), "private-secret") {
				t.Fatal("missing or unsafe error", err)
			}
		})
	}
	c := NewClient(Credentials{}, nil)
	c.baseURL = ":bad"
	if c.Authenticate(context.Background()) == nil {
		t.Fatal("bad URL accepted")
	}
	c.baseURL = "http://127.0.0.1:1"
	if c.Authenticate(context.Background()) == nil {
		t.Fatal("network failure ignored")
	}
}
func TestGetRefreshAndMutationNotRetried(t *testing.T) {
	for _, refreshFails := range []bool{false, true} {
		t.Run(fmt.Sprint(refreshFails), func(t *testing.T) {
			tokens, gets := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/oauth2/token" {
					tokens++
					if refreshFails && tokens > 1 {
						w.WriteHeader(401)
						return
					}
				}
				if r.URL.Path == "/v1/reporting/transactions" {
					gets++
					if gets == 1 {
						w.WriteHeader(401)
						return
					}
				}
				normalAPI(t, w, r)
			})
			if err := c.Authenticate(context.Background()); err != nil {
				t.Fatal(err)
			}
			_, _, err := c.Transactions(context.Background(), testQuery)
			if tokens != 2 || (refreshFails && err == nil) || (!refreshFails && (err != nil || gets != 2)) {
				t.Fatal(tokens, gets, err)
			}
		})
	}
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(401) })
	if c.postJSON(context.Background(), "/v2/checkout/orders", "key", []byte("{}"), new(any)) == nil || calls != 1 {
		t.Fatal("mutation retried")
	}
}
func TestReportingValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"missing list", `{"page":1,"total_pages":1}`, false},
		{"empty omitted", `{"page":1,"total_pages":0,"total_items":0}`, true},
		{"empty array", `{"transaction_details":[],"page":1,"total_pages":0}`, true},
		{"missing identity", `{"transaction_details":[{}],"page":1,"total_pages":1}`, false},
		{"bad page", `{"transaction_details":[],"page":0,"total_pages":1}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) })
			_, done, err := c.Transactions(context.Background(), testQuery)
			if tc.ok != (err == nil) || tc.ok != done {
				t.Fatal(done, err)
			}
		})
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"transaction_details":[],"page":%s,"total_pages":2}`, r.URL.Query().Get("page"))
	})
	q := testQuery
	q.MaxPages = 1
	if _, done, err := c.Transactions(context.Background(), q); err != nil || done {
		t.Fatal("page limit ignored")
	}
	c = testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) })
	if _, err := c.Webhooks(context.Background()); err == nil {
		t.Fatal("invalid webhook response accepted")
	}
	c = testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	if _, err := c.Webhooks(context.Background()); err == nil {
		t.Fatal("webhook error ignored")
	}
}
func TestOrderResponseValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		capture    bool
	}{
		{"bad create", `{}`, false},
		{"missing approval", `{"id":"ORDER1","status":"CREATED","links":[{"rel":"self","href":"https://api-m.sandbox.paypal.com"}]}`, false},
		{"live approval", `{"id":"ORDER1","status":"CREATED","links":[{"rel":"approve","href":"https://www.paypal.com/checkoutnow"}]}`, false},
		{"bad capture", `{"id":"OTHER","status":"COMPLETED"}`, true},
		{"missing captures", `{"id":"ORDER1","status":"COMPLETED"}`, true},
		{"bad capture ID", `{"id":"ORDER1","status":"COMPLETED","purchase_units":[{"payments":{"captures":[{"status":"COMPLETED"}]}}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) })
			var err error
			if tc.capture {
				_, err = c.CaptureOrder(context.Background(), "ORDER1", "key")
			} else {
				_, err = c.CreateOrder(context.Background(), "1.00", "USD", "http://localhost", "http://localhost", "key")
			}
			if err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	if _, err := c.CreateOrder(context.Background(), "1", "USD", "http://localhost", "http://localhost", "key"); err == nil {
		t.Fatal("create error ignored")
	}
	if _, err := c.CaptureOrder(context.Background(), "ORDER1", "key"); err == nil {
		t.Fatal("capture error ignored")
	}
}
func TestRedirectDenied(t *testing.T) {
	calls := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer target.Close()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) })
	if c.Authenticate(context.Background()) == nil || calls != 0 {
		t.Fatal("followed redirect")
	}
}
func TestSandboxCredentials(t *testing.T) {
	dir, err := os.MkdirTemp(".", ".test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.json")
	if _, err := LoadSandboxCredentials(path); err == nil {
		t.Fatal("missing file accepted")
	}
	for _, raw := range []string{"{", `{} {}`, `{"environment":"live"}`, `{"environment":"sandbox"}`} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadSandboxCredentials(path); err == nil {
			t.Fatal("bad credentials accepted")
		}
	}
	body, _ := json.Marshal(Credentials{Environment: "sandbox", ClientID: "id", ClientSecret: "secret"})
	os.WriteFile(path, body, 0600)
	t.Setenv("PAYPAL_CLIENT_ID", "override")
	t.Setenv("PAYPAL_CLIENT_SECRET", "override-secret")
	c, err := LoadSandboxCredentials(path)
	if err != nil || c.ClientID != "override" || c.ClientSecret != "override-secret" {
		t.Fatal(c, err)
	}
}

func TestPaymentAPIsRejectLiveBeforeRequest(t *testing.T) {
	calls := 0
	c := testClient(t, func(http.ResponseWriter, *http.Request) { calls++ })
	c.config.Environment = "live"
	if _, err := c.CreateOrder(context.Background(), "1", "USD", "http://localhost", "http://localhost", "key"); err == nil {
		t.Fatal("live order accepted")
	}
	if _, err := c.CaptureOrder(context.Background(), "ORDER1", "key"); err == nil {
		t.Fatal("live capture accepted")
	}
	if calls != 0 {
		t.Fatal("sandbox-only payment APIs made a live request")
	}
}
