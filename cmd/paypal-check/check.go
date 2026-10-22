package main

import (
	"context"
	"net/http"
	"paypal/server/internal/paypal"
	"regexp"
	"sort"
	"time"
)

const (
	Confirmed  = "confirmed"
	Failed     = "failed"
	Unverified = "unverified"
)

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type Report struct {
	Environment        string   `json:"environment"`
	StartAt            string   `json:"start_at"`
	EndAt              string   `json:"end_at"`
	Checks             []Check  `json:"checks"`
	TransactionCount   int      `json:"transaction_count"`
	ObservedCodes      []string `json:"observed_transaction_event_codes"`
	SubscriptionEvents []string `json:"subscription_events"`
	/* Temporarily disabled: identity mappings and subsequent checks.
	MatchedSamples     int      `json:"matched_samples"`
	UnresolvedSamples  int      `json:"unresolved_samples"`
	*/

}

// Run performs read-only checks. It never registers webhooks or persists payments.
func Run(ctx context.Context, c Config, h *http.Client) (Report, error) {
	var err error
	c, err = c.resolveRange(time.Now())
	if err != nil {
		return Report{}, err
	}
	if err := c.Validate(); err != nil {
		return Report{}, err
	}
	/* Temporarily disabled: identity mappings and subsequent checks.
	if err := samples.validate(c.Environment); err != nil {
		return Report{}, err
	}

	*/
	return newChecker(c, h).check(ctx), nil
}

func (c *checker) check(ctx context.Context) Report {
	r := Report{Environment: c.config.Environment, StartAt: c.config.StartAt, EndAt: c.config.EndAt,
		ObservedCodes: []string{}, SubscriptionEvents: []string{}, Checks: []Check{
			{"authorization", Unverified, "No OAuth evidence yet."},
			{"history_access", Unverified, "Requires successful authorization."},
			{"sandbox_coverage", Unverified, "Requires completed sandbox history retrieval and expected transaction event codes."},
			{"webhook_subscriptions", Unverified, "Requires successful authorization."},
			/* Temporarily disabled: identity mappings and subsequent checks.
			{"identity_mappings", Unverified, "Requires history and same-environment resource samples; sample authenticity is not verified."},
			{"consumer_history_access", Unverified, "App-level API access does not establish third-party consumer consent or PayPal partner approval."},
			{"source_record_identity", Unverified, "A general stable reporting-entry key requires further validation, including balance-affecting and non-balance-affecting records."},
			*/

		}}
	if err := c.api.Authenticate(ctx); err != nil {
		r.Checks[0] = Check{"authorization", Failed, err.Error()}
		return r
	}
	r.Checks[0] = Check{"authorization", Confirmed, "OAuth credentials accepted for the selected application and environment."}
	records, complete, historyErr := c.api.Transactions(ctx, paypal.Query{StartAt: c.config.StartAt, EndAt: c.config.EndAt, MaxPages: c.config.MaxPages})
	r.TransactionCount = len(records)
	codes := map[string]bool{}
	for _, tx := range records {
		if codePattern.MatchString(tx.Info.Code) {
			codes[tx.Info.Code] = true
		}
	}
	r.ObservedCodes = sortedKeys(codes)
	if historyErr != nil {
		r.Checks[1] = Check{"history_access", Failed, historyErr.Error()}
	} else if !complete {
		r.Checks[1] = Check{"history_access", Unverified, "Page limit reached; increase max_pages or narrow the date range. Partial observations are not complete coverage."}
	} else {
		r.Checks[1] = Check{"history_access", Confirmed, "Transaction Search accepted the request for this app/account and date range; this does not establish third-party history access."}
		if c.config.Environment == "sandbox" && len(records) > 0 && len(c.config.ExpectedCodes) > 0 {
			all := true
			for _, code := range c.config.ExpectedCodes {
				all = all && codes[code]
			}
			if all {
				r.Checks[2] = Check{"sandbox_coverage", Confirmed, "All configured transaction event codes were observed in the queried sandbox range; this is not proof of complete consumer coverage."}
			}
		}
		/* Temporarily disabled: identity mappings and subsequent checks.
		r.MatchedSamples, r.UnresolvedSamples = matchSamples(records, samples)
		if r.MatchedSamples+r.UnresolvedSamples > 0 {
			detail := "Some samples lack a unique explicit reporting relationship; retain them for reconciliation. Sample authenticity is not verified."
			status := Unverified
			if r.UnresolvedSamples == 0 {
				status = Confirmed
				detail = "All supplied samples have unique explicit identifier relationships in this response. This confirms sample-level matches only, not sample authenticity or a general deduplication key."
			}
			r.Checks[4] = Check{"identity_mappings", status, detail}
		}
		*/

	}
	hooks, err := c.api.Webhooks(ctx)
	if err != nil {
		r.Checks[3] = Check{"webhook_subscriptions", Failed, err.Error()}
		return r
	}
	events := map[string]bool{}
	matchedURL := false
	for _, hook := range hooks {
		if c.config.WebhookURL == "" || hook.URL != c.config.WebhookURL {
			continue
		}
		matchedURL = true
		for _, event := range hook.Events {
			if event.Name == "*" || eventPattern.MatchString(event.Name) {
				events[event.Name] = true
			}
		}
	}
	r.SubscriptionEvents = sortedKeys(events)
	r.Checks[3] = Check{"webhook_subscriptions", Unverified, "Specify webhook_url and expected_webhook_events to check an existing listener. No registration was changed."}
	if c.config.WebhookURL != "" && len(c.config.ExpectedEvents) > 0 {
		all := matchedURL
		for _, event := range c.config.ExpectedEvents {
			all = all && (events[event] || events["*"])
		}
		if all {
			r.Checks[3] = Check{"webhook_subscriptions", Confirmed, "The configured listener has all expected event subscriptions; event delivery and signature verification remain unverified."}
		} else {
			r.Checks[3] = Check{"webhook_subscriptions", Failed, "The configured listener or required subscriptions are missing; provision them during environment setup."}
		}
	}
	return r
}

var codePattern = regexp.MustCompile(`^T[0-9]{4}$`)
var eventPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*(\.[A-Z0-9_]+)+$`)

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (r Report) HasFailures() bool {
	for _, check := range r.Checks {
		if check.Status == Failed {
			return true
		}
	}
	return false
}

/* Temporarily disabled: identity-mapping sample types and helpers.
// Local samples keep API resource shapes. No payment amounts, payloads or IDs enter the report.
type Resource struct {
	Links []struct {
		Rel  string `json:"rel"`
		Href string `json:"href"`
	} `json:"links"`
	ID      string `json:"id"`
	Related struct {
		IDs struct {
			CaptureID string `json:"capture_id"`
		} `json:"related_ids"`
	} `json:"supplementary_data"`
}

type Event struct {
	ResourceType string   `json:"resource_type"`
	Type         string   `json:"event_type"`
	Resource     Resource `json:"resource"`
}

type Samples struct {
	Environment string     `json:"environment"`
	Captures    []Resource `json:"captures"`
	Refunds     []Resource `json:"refunds"`
	Webhooks    []Event    `json:"webhooks"`
}

func LoadSamples(path string) (Samples, error) {
	var s Samples
	if path == "" {
		return s, nil
	}
	err := readJSON(path, &s, false)
	return s, err
}

func matchSamples(records []transaction, s Samples) (matched, unresolved int) {
	byID := map[string][]transaction{}
	for _, record := range records {
		byID[record.Info.ID] = append(byID[record.Info.ID], record)
	}
	fmt.Printf("debug %+v %+v", records, s)
	match := func(resource Resource, refund bool) bool {
		candidates := byID[resource.ID]
		if resource.ID == "" || len(candidates) != 1 {
			return false
		}
		capture := resource.Related.IDs.CaptureID
		for _, link := range resource.Links {
			if link.Rel != "up" {
				continue
			}
			u, err := url.Parse(link.Href)
			validHost := err == nil && ((s.Environment == "sandbox" && (u.Host == "api-m.sandbox.paypal.com" || u.Host == "api.sandbox.paypal.com")) || (s.Environment == "live" && (u.Host == "api-m.paypal.com" || u.Host == "api.paypal.com")))
			if !validHost || u.Scheme != "https" {
				return false
			}
			const prefix = "/v2/payments/captures/"
			id := strings.TrimPrefix(u.Path, prefix)
			if id == u.Path || id == "" || strings.Contains(id, "/") || (capture != "" && capture != id) {
				return false
			}
			capture = id
		}
		return !refund || (capture != "" && candidates[0].Info.Reference == capture && candidates[0].Info.ReferenceType == "TXN")
	}
	add := func(ok bool) {
		if ok {
			matched++
		} else {
			unresolved++
		}
	}
	for _, resource := range s.Captures {
		add(match(resource, false))
	}
	for _, resource := range s.Refunds {
		add(match(resource, true))
	}
	for _, event := range s.Webhooks {
		switch {
		case event.ResourceType == "capture" && strings.HasPrefix(event.Type, "PAYMENT.CAPTURE."):
			add(match(event.Resource, false))
		case event.ResourceType == "refund" && (strings.HasPrefix(event.Type, "PAYMENT.REFUND.") || event.Type == "PAYMENT.CAPTURE.REFUNDED"):
			add(match(event.Resource, true))
		default:
			add(false)
		}
	}
	return
}

*/

// checker owns diagnostic policy and report assembly; paypal.Client owns API access.
type checker struct {
	config Config
	api    *paypal.Client
}

func newChecker(c Config, h *http.Client) *checker {
	return &checker{config: c, api: paypal.NewClient(paypal.Credentials{Environment: c.Environment, ClientID: c.ClientID, ClientSecret: c.ClientSecret}, h)}
}
