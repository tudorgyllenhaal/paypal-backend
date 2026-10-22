package paypalcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{Environment: "sandbox", ClientID: "secret-client", ClientSecret: "secret-password", StartAt: "2026-10-01T00:00:00Z", EndAt: "2026-10-02T00:00:00Z", MaxPages: 3, WebhookURL: "https://listener.example/paypal", ExpectedEvents: []string{"PAYMENT.CAPTURE.COMPLETED"}, ExpectedCodes: []string{"T0006"}}
}
func fixture(t *testing.T, content string) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", ".test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "input.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func configFile(t *testing.T, c Config) string {
	t.Helper()
	b, _ := json.Marshal(c)
	return fixture(t, string(b))
}
func mock(t *testing.T, handler http.HandlerFunc) *client {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	c := newClient(validConfig(), s.Client())
	c.baseURL = s.URL
	return c
}

const tokenJSON = `{"access_token":"secret-token","token_type":"Bearer"}`
const recordsJSON = `{"transaction_details":[{"transaction_info":{"transaction_id":"private-capture","transaction_event_code":"T0006"}},{"transaction_info":{"transaction_id":"private-refund","transaction_event_code":"T1107","paypal_reference_id":"private-capture","paypal_reference_id_type":"TXN"}}],"page":1,"total_pages":1}`
const hooksJSON = `{"webhooks":[{"url":"https://listener.example/paypal","event_types":[{"name":"PAYMENT.CAPTURE.COMPLETED"}]}]}`

func normal(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	switch r.URL.Path {
	case "/v1/oauth2/token":
		user, pass, ok := r.BasicAuth()
		if !ok || user != "secret-client" || pass != "secret-password" {
			t.Error("wrong credentials")
		}
		r.ParseForm()
		if r.Method != "POST" || r.Form.Get("grant_type") != "client_credentials" {
			t.Error("wrong OAuth request")
		}
		fmt.Fprint(w, tokenJSON)
	case "/v1/reporting/transactions":
		if r.Header.Get("Authorization") != "Bearer secret-token" || r.Method != "GET" {
			t.Error("wrong authorization")
		}
		q := r.URL.Query()
		if q.Get("balance_affecting_records_only") != "N" || q.Get("page_size") != "500" || q.Get("start_date") == "" || q.Get("end_date") == "" {
			t.Error("wrong history query")
		}
		fmt.Fprint(w, recordsJSON)
	case "/v1/notifications/webhooks":
		fmt.Fprint(w, hooksJSON)
	default:
		t.Errorf("unexpected endpoint %s", r.URL.Path)
		w.WriteHeader(404)
	}
}

/*
Temporarily disabled: identity mappings and subsequent checks.

	func samples(t *testing.T) Samples {
		t.Helper()
		s, err := LoadSamples(fixture(t, `{"environment":"sandbox","captures":[{"id":"private-capture","amount":{"value":"100.00"}}],"refunds":[{"id":"private-refund","links":[{"rel":"up","href":"https://api-m.sandbox.paypal.com/v2/payments/captures/private-capture"}]}],"webhooks":[{"event_type":"PAYMENT.CAPTURE.COMPLETED","resource_type":"capture","resource":{"id":"private-capture"}},{"event_type":"PAYMENT.CAPTURE.REFUNDED","resource_type":"refund","resource":{"id":"private-refund","supplementary_data":{"related_ids":{"capture_id":"private-capture"}}}}]}`))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
*/
func TestSuccessfulChecks(t *testing.T) {
	c := mock(t, func(w http.ResponseWriter, r *http.Request) { normal(t, w, r) })
	report := c.check(context.Background())
	for _, i := range []int{0, 1, 2, 3} {
		if report.Checks[i].Status != Confirmed {
			t.Fatalf("%+v", report.Checks)
		}
	}
	if report.HasFailures() || report.TransactionCount != 2 {
		t.Fatalf("%+v", report)
	}
	/* Temporarily disabled: identity mappings and subsequent checks.
	if report.Checks[5].Status != Unverified || report.Checks[6].Status != Unverified {
		t.Fatal("overstated access or stable identity")
	}

	*/
	b, _ := json.Marshal(report)
	for _, secret := range []string{"secret-client", "secret-password", "secret-token", "private-capture", "private-refund", "100.00"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("report leaked %s", secret)
		}
	}
}
func TestAPIFailures(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		status, index    int
	}{
		{"bad credentials", "/v1/oauth2/token", `{"secret":"secret-password"}`, 401, 0},
		{"missing permission", "/v1/reporting/transactions", "", 403, 1},
		{"rate limit", "/v1/reporting/transactions", "", 429, 1},
		{"server error", "/v1/reporting/transactions", "", 500, 1},
		{"webhook permission", "/v1/notifications/webhooks", "", 403, 3},
		{"malformed history", "/v1/reporting/transactions", "{", 200, 1},
		{"trailing JSON", "/v1/reporting/transactions", recordsJSON + ` {}`, 200, 1},
		{"missing token", "/v1/oauth2/token", `{"token_type":"Bearer"}`, 200, 0},
		{"missing webhook list", "/v1/notifications/webhooks", `{}`, 200, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := mock(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tc.path {
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.body)
					return
				}
				normal(t, w, r)
			})
			report := c.check(context.Background())
			if !report.HasFailures() || report.Checks[tc.index].Status != Failed {
				t.Fatalf("%+v", report.Checks)
			}
			b, _ := json.Marshal(report)
			if strings.Contains(string(b), "secret-password") {
				t.Fatal("leaked response body")
			}
		})
	}
}
func TestRefreshToken(t *testing.T) {
	for _, failRefresh := range []bool{false, true} {
		t.Run(fmt.Sprint(failRefresh), func(t *testing.T) {
			tokens, requests := 0, 0
			c := mock(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/oauth2/token" {
					tokens++
					if failRefresh && tokens > 1 {
						w.WriteHeader(401)
						return
					}
				}
				if r.URL.Path == "/v1/reporting/transactions" {
					requests++
					if requests == 1 {
						w.WriteHeader(401)
						return
					}
				}
				normal(t, w, r)
			})
			report := c.check(context.Background())
			if tokens != 2 {
				t.Fatalf("tokens=%d", tokens)
			}
			if failRefresh {
				if report.Checks[1].Status != Failed {
					t.Fatal(report)
				}
			} else if report.Checks[1].Status != Confirmed || requests != 2 {
				t.Fatal(report)
			}
		})
	}
}
func TestPagination(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		limit      int
		status     string
		count      int
	}{
		{"multiple pages", "", 3, Confirmed, 2},
		{"page limit", "", 1, Unverified, 1},
		{"empty", `{"transaction_details":[],"page":1,"total_pages":0}`, 3, Confirmed, 0},
		{"omitted empty list", `{"page":1,"total_pages":0,"total_items":0}`, 3, Confirmed, 0},
		{"null empty list", `{"transaction_details":null,"page":1,"total_pages":0,"total_items":0}`, 3, Confirmed, 0},
		{"missing list with items", `{"page":1,"total_pages":0,"total_items":1}`, 3, Failed, 0},
		{"missing list without totals", `{"page":1,"total_pages":0}`, 3, Failed, 0},
		{"missing list", `{"page":1,"total_pages":1}`, 3, Failed, 0},
		{"wrong page", `{"transaction_details":[],"page":2,"total_pages":1}`, 3, Failed, 0},
		{"missing identity", `{"transaction_details":[{"transaction_info":{}}],"page":1,"total_pages":1}`, 3, Failed, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := mock(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/reporting/transactions" {
					if tc.body != "" {
						fmt.Fprint(w, tc.body)
					} else {
						fmt.Fprintf(w, `{"transaction_details":[{"transaction_info":{"transaction_id":"id-%s","transaction_event_code":"T0006"}}],"page":%s,"total_pages":2}`, r.URL.Query().Get("page"), r.URL.Query().Get("page"))
					}
					return
				}
				normal(t, w, r)
			})
			c.config.MaxPages = tc.limit
			report := c.check(context.Background())
			if report.Checks[1].Status != tc.status || report.TransactionCount != tc.count {
				t.Fatalf("%+v", report)
			}
			if tc.name == "empty" && report.Checks[2].Status != Unverified {
				t.Fatal("empty data claimed coverage")
			}
		})
	}
}
func TestSubscriptionAndCoverageUncertainty(t *testing.T) {
	for _, name := range []string{"missing listener", "missing subscription", "no expectations", "live", "missing expected code"} {
		t.Run(name, func(t *testing.T) {
			c := mock(t, func(w http.ResponseWriter, r *http.Request) { normal(t, w, r) })
			switch name {
			case "missing listener":
				c.config.WebhookURL = "https://other.example/paypal"
			case "missing subscription":
				c.config.ExpectedEvents = []string{"PAYMENT.CAPTURE.DENIED"}
			case "no expectations":
				c.config.WebhookURL = ""
				c.config.ExpectedCodes = nil
			case "live":
				c.config.Environment = "live"
			case "missing expected code":
				c.config.ExpectedCodes = []string{"T9999"}
			}
			report := c.check(context.Background())
			if strings.HasPrefix(name, "missing l") || name == "missing subscription" {
				if report.Checks[3].Status != Failed {
					t.Fatal(report)
				}
			} else if report.Checks[2].Status != Unverified {
				t.Fatal(report)
			}
		})
	}
}

/*
Temporarily disabled: identity mappings and subsequent checks.

	func TestAmbiguousMappings(t *testing.T) {
		c := mock(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/reporting/transactions" {
				fmt.Fprint(w, `{"transaction_details":[{"transaction_info":{"transaction_id":"private-capture","transaction_event_code":"T0006"}},{"transaction_info":{"transaction_id":"private-capture","transaction_event_code":"T0006"}}],"page":1,"total_pages":1}`)
				return
			}
			normal(t, w, r)
		})
		report := c.check(context.Background())
		if report.Checks[4].Status != Unverified || report.UnresolvedSamples != 4 {
			t.Fatalf("%+v", report)
		}
	}

	func TestSampleRelationships(t *testing.T) {
		var records []transaction
		var body struct {
			Records []transaction `json:"transaction_details"`
		}
		json.Unmarshal([]byte(recordsJSON), &body)
		records = body.Records
		for _, raw := range []string{
			`{"refunds":[{"id":"private-refund"}]}`,
			`{"environment":"sandbox","refunds":[{"id":"private-refund","links":[{"rel":"up","href":"https://api-m.paypal.com/v2/payments/captures/private-capture"}]}]}`,
			`{"refunds":[{"id":"private-refund","links":[{"rel":"up","href":"https://evil.example/v2/payments/captures/private-capture"}]}]}`,
			`{"refunds":[{"id":"private-refund","links":[{"rel":"up","href":"https://api-m.sandbox.paypal.com/not-a-capture"}]}]}`,
			`{"refunds":[{"id":"private-refund","supplementary_data":{"related_ids":{"capture_id":"other"}},"links":[{"rel":"up","href":"https://api-m.sandbox.paypal.com/v2/payments/captures/private-capture"}]}]}`,
			`{"webhooks":[{"event_type":"UNKNOWN","resource":{"id":"private-capture"}}]}`,
		} {
			var s Samples
			json.Unmarshal([]byte(raw), &s)
			if s.Environment == "" {
				s.Environment = "sandbox"
			}
			matched, unresolved := matchSamples(records, s)
			if matched != 0 || unresolved != 1 {
				t.Fatalf("accepted unresolved sample: %s", raw)
			}
		}
	}
*/
type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRunValidationAndNetworkFailure(t *testing.T) {
	c := validConfig()
	if _, err := Run(context.Background(), Config{}, nil); err == nil {
		t.Fatal("invalid config accepted")
	}
	/* Temporarily disabled: identity mappings and subsequent checks.
	if _, err := Run(context.Background(), c, Samples{Environment: "live", Captures: []Resource{{ID: "id"}}}, nil); err == nil {
		t.Fatal("mixed samples accepted")
	}

	*/
	h := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("secret-password") })}
	report, err := Run(context.Background(), c, h)
	if err != nil || !report.HasFailures() || strings.Contains(report.Checks[0].Detail, "secret-password") {
		t.Fatal(report, err)
	}
	live := c
	live.Environment = "live"
	if newClient(live, nil).baseURL != "https://api-m.paypal.com" {
		t.Fatal("wrong live host")
	}
	bad := newClient(c, h)
	bad.baseURL = ":bad"
	if err := bad.authenticate(context.Background()); err == nil {
		t.Fatal("invalid URL accepted")
	}
}
func TestRedirectNotFollowed(t *testing.T) {
	calls := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer target.Close()
	c := mock(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) })
	if !c.check(context.Background()).HasFailures() || calls != 0 {
		t.Fatal("followed redirect")
	}
}
func TestConfigAndSampleFiles(t *testing.T) {
	c := validConfig()
	loaded, err := LoadConfig(configFile(t, c))
	if err != nil || loaded.ClientID != c.ClientID {
		t.Fatal(loaded, err)
	}
	t.Setenv("PAYPAL_CLIENT_ID", "env-id")
	t.Setenv("PAYPAL_CLIENT_SECRET", "env-secret")
	loaded, err = LoadConfig(configFile(t, c))
	if err != nil || loaded.ClientID != "env-id" || loaded.ClientSecret != "env-secret" {
		t.Fatal(err)
	}
	if _, err = LoadConfig("not-here.json"); err == nil {
		t.Fatal("missing file accepted")
	}
	for _, raw := range []string{"{", `{} {}`, `{"unexpected":true}`} {
		if _, err = LoadConfig(fixture(t, raw)); err == nil {
			t.Fatal("invalid JSON/config accepted")
		}
	}
	/* Temporarily disabled: identity mappings and subsequent checks.
	if _, err = LoadSamples(""); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadSamples(fixture(t, "{")); err == nil {
		t.Fatal("invalid samples accepted")
	}
	*/

}
func TestConfigValidation(t *testing.T) {
	for _, name := range []string{"environment", "id", "secret", "start", "end", "reverse", "window", "pages", "url", "event", "code"} {
		t.Run(name, func(t *testing.T) {
			c := validConfig()
			switch name {
			case "environment":
				c.Environment = "bad"
			case "id":
				c.ClientID = ""
			case "secret":
				c.ClientSecret = ""
			case "start":
				c.StartAt = "bad"
			case "end":
				c.EndAt = "bad"
			case "reverse":
				c.EndAt = c.StartAt
			case "window":
				c.EndAt = "2026-12-01T00:00:00Z"
			case "pages":
				c.MaxPages = 0
			case "url":
				c.WebhookURL = "http://user:pass@example.com"
			case "event":
				c.ExpectedEvents = []string{"bad"}
			case "code":
				c.ExpectedCodes = []string{"bad"}
			}
			if c.Validate() == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestWildcardAndPersistentUnauthorized(t *testing.T) {
	t.Run("wildcard subscription", func(t *testing.T) {
		c := mock(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/notifications/webhooks" {
				fmt.Fprint(w, `{"webhooks":[{"url":"https://listener.example/paypal","event_types":[{"name":"*"}]}]}`)
				return
			}
			normal(t, w, r)
		})
		if r := c.check(context.Background()); r.Checks[3].Status != Confirmed {
			t.Fatal(r)
		}
	})
	t.Run("retry once", func(t *testing.T) {
		calls := 0
		c := mock(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/reporting/transactions" {
				calls++
				w.WriteHeader(401)
				return
			}
			normal(t, w, r)
		})
		if r := c.check(context.Background()); r.Checks[1].Status != Failed || calls != 2 {
			t.Fatal(r, calls)
		}
	})
	/* Temporarily disabled: identity mappings and subsequent checks.
	t.Run("same environment accepted", func(t *testing.T) {
		h := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("offline") })}
		if _, err := Run(context.Background(), validConfig(), samples(t), h); err != nil {
			t.Fatal(err)
		}
	})
	*/

}

func TestRollingRange(t *testing.T) {
	now := time.Date(2026, 10, 22, 18, 34, 56, 999, time.FixedZone("test", 8*3600))
	for _, tc := range []struct {
		name       string
		hours      int
		start, end string
		wantHours  int
		wantErr    bool
	}{
		{"default", 0, "", "", 24, false},
		{"rolling overrides stale dates", 48, "2026-10-01T00:00:00Z", "2026-10-02T00:00:00Z", 48, false},
		{"maximum", 744, "", "", 744, false},
		{"negative", -1, "", "", 0, true},
		{"too large", 745, "", "", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			c.LookbackHours = tc.hours
			c.StartAt = tc.start
			c.EndAt = tc.end
			resolved, err := c.resolveRange(now)
			if tc.wantErr {
				if err == nil {
					t.Fatal("invalid window accepted")
				}
				if c.Validate() == nil {
					t.Fatal("invalid lookback validated")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			end, _ := time.Parse(time.RFC3339, resolved.EndAt)
			start, _ := time.Parse(time.RFC3339, resolved.StartAt)
			if !end.Equal(now.Truncate(time.Second)) || end.Sub(start) != time.Duration(tc.wantHours)*time.Hour || !strings.HasSuffix(resolved.EndAt, "Z") {
				t.Fatalf("unexpected UTC range: %s to %s", resolved.StartAt, resolved.EndAt)
			}
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
	c := validConfig()
	resolved, err := c.resolveRange(now)
	if err != nil || resolved.StartAt != c.StartAt || resolved.EndAt != c.EndAt {
		t.Fatal("explicit dates changed")
	}
}

func TestRunUsesOneRollingRangeForAllPages(t *testing.T) {
	var ranges []string
	mockClient := mock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/reporting/transactions" {
			q := r.URL.Query()
			ranges = append(ranges, q.Get("start_date")+"/"+q.Get("end_date"))
			fmt.Fprintf(w, `{"transaction_details":[],"page":%s,"total_pages":2}`, q.Get("page"))
			return
		}
		normal(t, w, r)
	})
	// Route the exported runner's fixed PayPal host to the local mock server.
	target, _ := url.Parse(mockClient.baseURL)
	h := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = target.Scheme
		u.Host = target.Host
		clone.URL = &u
		return http.DefaultTransport.RoundTrip(clone)
	})}
	c := validConfig()
	c.LookbackHours = 24
	before := time.Now().UTC().Truncate(time.Second)
	report, err := Run(context.Background(), c, h)
	after := time.Now().UTC().Truncate(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	start, _ := time.Parse(time.RFC3339, report.StartAt)
	end, _ := time.Parse(time.RFC3339, report.EndAt)
	if end.Before(before) || end.After(after) || end.Sub(start) != 24*time.Hour {
		t.Fatal("rolling range not used in report")
	}
	if len(ranges) != 2 || ranges[0] != ranges[1] || ranges[0] != report.StartAt+"/"+report.EndAt {
		t.Fatal("pagination range changed", ranges)
	}
	c.LookbackHours = 745
	if _, err := Run(context.Background(), c, h); err == nil {
		t.Fatal("invalid rolling range accepted")
	}
}

func TestLoadRollingConfig(t *testing.T) {
	c := validConfig()
	c.LookbackHours = 24
	c.StartAt = ""
	c.EndAt = ""
	loaded, err := LoadConfig(configFile(t, c))
	if err != nil || loaded.LookbackHours != 24 {
		t.Fatal("rolling configuration not loaded", err)
	}
}

func TestOnlyEnabledChecksReported(t *testing.T) {
	c := mock(t, func(w http.ResponseWriter, r *http.Request) { normal(t, w, r) })
	report := c.check(context.Background())
	expected := []string{"authorization", "history_access", "sandbox_coverage", "webhook_subscriptions"}
	if len(report.Checks) != len(expected) {
		t.Fatalf("unexpected checks: %+v", report.Checks)
	}
	for i, name := range expected {
		if report.Checks[i].Name != name {
			t.Fatalf("unexpected check %s", report.Checks[i].Name)
		}
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"identity_mappings", "consumer_history_access", "source_record_identity", "matched_samples", "unresolved_samples"} {
		if strings.Contains(string(data), field) {
			t.Fatalf("disabled field still reported: %s", field)
		}
	}
}
