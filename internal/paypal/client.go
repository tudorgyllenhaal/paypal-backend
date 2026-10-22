package paypal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client handles PayPal authentication and API requests.
type Client struct {
	config  Credentials
	http    *http.Client
	baseURL string
	token   string
}

type apiError struct{ status int }

func (e apiError) Error() string {
	switch e.status {
	case 401:
		return "PayPal rejected credentials or the access token; check the selected app and environment"
	case 403:
		return "PayPal denied permission; verify Transaction Search/partner access or webhook permissions for this app"
	case 422:
		return "PayPal rejected the operation; ensure the buyer approved the order, funds and currency are valid, and the order has not already been captured"
	case 429:
		return "PayPal rate limit reached; retry later"
	default:
		return fmt.Sprintf("PayPal returned HTTP %d; check the date range, API availability and account permissions", e.status)
	}
}

// NewClient selects the PayPal environment and disables redirects.
func NewClient(c Credentials, httpClient *http.Client) *Client {
	h := http.Client{Timeout: 20 * time.Second}
	if httpClient != nil {
		h = *httpClient
		if h.Timeout == 0 {
			h.Timeout = 20 * time.Second
		}
	}
	h.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	base := "https://api-m.sandbox.paypal.com"
	if c.Environment == "live" {
		base = "https://api-m.paypal.com"
	}
	return &Client{config: c, http: &h, baseURL: base}
}

func (c *Client) request(ctx context.Context, method, path string, body io.Reader, dst any, auth bool, requestID string) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("cannot construct PayPal request")
	}
	req.Header.Set("Accept", "application/json")
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.token)
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("PayPal-Request-Id", requestID)
			req.Header.Set("Prefer", "return=representation")
		}
	} else {
		req.SetBasicAuth(c.config.ClientID, c.config.ClientSecret)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("PayPal request failed; check network connectivity or timeout")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && !(auth && method == http.MethodPost && resp.StatusCode == http.StatusCreated) {
		return apiError{status: resp.StatusCode}
	}
	d := json.NewDecoder(io.LimitReader(resp.Body, 4<<20))
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("PayPal returned invalid JSON")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("PayPal returned trailing or oversized JSON")
	}

	return nil
}

// Authenticate obtains an application access token without exposing it to callers.
func (c *Client) Authenticate(ctx context.Context) error {
	var response struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := c.request(ctx, http.MethodPost, "/v1/oauth2/token", strings.NewReader("grant_type=client_credentials"), &response, false, ""); err != nil {
		return err
	}
	if response.AccessToken == "" || !strings.EqualFold(response.TokenType, "Bearer") {
		return fmt.Errorf("PayPal returned an invalid OAuth token response")
	}
	c.token = response.AccessToken
	return nil
}

func (c *Client) get(ctx context.Context, path string, dst any) error {
	err := c.request(ctx, http.MethodGet, path, nil, dst, true, "")
	if e, ok := err.(apiError); ok && e.status == 401 {
		if err := c.Authenticate(ctx); err != nil {
			return err
		}
		return c.request(ctx, http.MethodGet, path, nil, dst, true, "")
	}
	return err
}

type Transaction struct {
	Info struct {
		ID            string `json:"transaction_id"`
		Code          string `json:"transaction_event_code"`
		Reference     string `json:"paypal_reference_id"`
		ReferenceType string `json:"paypal_reference_id_type"`
	} `json:"transaction_info"`
}

func (c *Client) Transactions(ctx context.Context, query Query) ([]Transaction, bool, error) {
	records := []Transaction{}
	for page := 1; page <= query.MaxPages; page++ {
		q := url.Values{"start_date": {query.StartAt}, "end_date": {query.EndAt}, "fields": {"transaction_info"}, "balance_affecting_records_only": {"N"}, "page_size": {"500"}, "page": {fmt.Sprint(page)}}
		var response struct {
			Records    *[]Transaction `json:"transaction_details"`
			Page       int            `json:"page"`
			TotalPages int            `json:"total_pages"`
			TotalItems *int           `json:"total_items"`
		}
		if err := c.get(ctx, "/v1/reporting/transactions?"+q.Encode(), &response); err != nil {
			return records, false, err
		}
		// PayPal omits transaction_details for a successful empty search.
		if page == 1 && response.Page == 1 && response.TotalPages == 0 && response.TotalItems != nil && *response.TotalItems == 0 && (response.Records == nil || len(*response.Records) == 0) {
			return records, true, nil
		}
		if response.Records == nil || response.Page != page || response.TotalPages < 0 || (response.TotalPages == 0 && len(*response.Records) > 0) || (response.TotalPages > 0 && response.TotalPages < page) {
			return records, false, fmt.Errorf("PayPal returned invalid transaction pagination")
		}
		for _, record := range *response.Records {
			if record.Info.ID == "" {
				return records, false, fmt.Errorf("PayPal returned a transaction without an identifier")
			}
		}
		records = append(records, (*response.Records)...)
		if page >= response.TotalPages {
			return records, true, nil
		}
	}
	return records, false, nil
}

type Webhook struct {
	URL    string `json:"url"`
	Events []struct {
		Name string `json:"name"`
	} `json:"event_types"`
}

func (c *Client) Webhooks(ctx context.Context) ([]Webhook, error) {
	var response struct {
		Webhooks *[]Webhook `json:"webhooks"`
	}
	if err := c.get(ctx, "/v1/notifications/webhooks", &response); err != nil {
		return nil, err
	}
	if response.Webhooks == nil {
		return nil, fmt.Errorf("PayPal returned an invalid webhook list")
	}
	return *response.Webhooks, nil
}

// Query contains a fixed date window shared by all reporting pages.
type Query struct {
	StartAt  string
	EndAt    string
	MaxPages int
}

// postJSON makes one authenticated mutation request without automatic retries.
func (c *Client) postJSON(ctx context.Context, path, requestID string, body []byte, dst any) error {
	if err := c.request(ctx, http.MethodPost, path, bytes.NewReader(body), dst, true, requestID); err != nil {
		return fmt.Errorf("%w; retry payment operations only with the same request-id and arguments", err)
	}
	return nil
}
