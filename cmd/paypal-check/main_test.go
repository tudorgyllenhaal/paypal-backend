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
	"strings"
	"testing"
)

func input(t *testing.T, text string) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", ".test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "input.json")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

const checkConfig = `{"environment":"sandbox","client_id":"id","client_secret":"secret","start_at":"2026-10-01T00:00:00Z","end_at":"2026-10-02T00:00:00Z","max_pages":1}`

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func mockAPI(t *testing.T, fail bool) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/v1/oauth2/token":
			fmt.Fprint(w, `{"access_token":"token","token_type":"Bearer"}`)
		case "/v1/reporting/transactions":
			fmt.Fprint(w, `{"transaction_details":[],"page":1,"total_pages":0}`)
		case "/v1/notifications/webhooks":
			fmt.Fprint(w, `{"webhooks":[]}`)
		default:
			t.Error("unexpected request")
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(s.Close)
	old := http.DefaultTransport
	target, _ := url.Parse(s.URL)
	http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = target.Scheme
		u.Host = target.Host
		clone.URL = &u
		return old.RoundTrip(clone)
	})
	t.Cleanup(func() { http.DefaultTransport = old })
}
func TestRunReport(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			mockAPI(t, fail)
			var out, errs bytes.Buffer
			code := run(context.Background(), []string{"-config", input(t, checkConfig)}, &out, &errs)
			want := 0
			if fail {
				want = 1
			}
			if code != want {
				t.Fatal(code, errs.String())
			}
			var report map[string]any
			if json.Unmarshal(out.Bytes(), &report) != nil {
				t.Fatal("missing JSON report")
			}
			if report["environment"] != "sandbox" {
				t.Fatal(report)
			}
		})
	}
}
func TestRunInputErrors(t *testing.T) {
	for _, name := range []string{"flags", "args", "config"} {
		t.Run(name, func(t *testing.T) {
			args := []string{"-config", input(t, checkConfig)}
			switch name {
			case "flags":
				args = []string{"-unknown"}
			case "args":
				args = append(args, "extra")
			case "config":
				args = []string{"-config", "not-found.json"}
				/* Temporarily disabled: identity mappings and subsequent checks.
				case "samples":
					args = append(args, "-samples", input(t, "{"))
				case "environment":
					args = append(args, "-samples", input(t, `{"environment":"live","captures":[{"id":"id"}]}`))
				*/

			}
			var out, errs bytes.Buffer
			if run(context.Background(), args, &out, &errs) != 1 || errs.Len() == 0 || out.Len() != 0 {
				t.Fatal("invalid input not rejected")
			}
		})
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("write failure") }
func TestRunOutputError(t *testing.T) {
	mockAPI(t, false)
	var errs bytes.Buffer
	if run(context.Background(), []string{"-config", input(t, checkConfig)}, brokenWriter{}, &errs) != 1 || errs.Len() == 0 {
		t.Fatal("write failure not reported")
	}
}

func TestSamplesOptionDisabled(t *testing.T) {
	var out, errs bytes.Buffer
	if run(context.Background(), []string{"-samples", "not-loaded.json"}, &out, &errs) != 1 || !strings.Contains(errs.String(), "flag provided but not defined") {
		t.Fatal("disabled samples flag was accepted")
	}
}
