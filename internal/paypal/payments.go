package paypal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)

type Capture struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
type PaymentResult struct {
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

func (c *Client) CreateOrder(ctx context.Context, amount, currency, returnURL, cancelURL, requestID string) (PaymentResult, error) {
	if c.config.Environment != "sandbox" {
		return PaymentResult{}, fmt.Errorf("payment test APIs only support sandbox")
	}
	body := map[string]any{"intent": "CAPTURE", "purchase_units": []any{map[string]any{"amount": map[string]string{"currency_code": currency, "value": amount}}}, "payment_source": map[string]any{"paypal": map[string]any{"experience_context": map[string]string{"return_url": returnURL, "cancel_url": cancelURL, "user_action": "PAY_NOW", "shipping_preference": "NO_SHIPPING"}}}}
	data, _ := json.Marshal(body)
	var response order
	if err := c.postJSON(ctx, "/v2/checkout/orders", requestID, data, &response); err != nil {
		return PaymentResult{}, err
	}
	if !idPattern.MatchString(response.ID) || response.Status == "" {
		return PaymentResult{}, fmt.Errorf("sandbox returned an invalid order response")
	}
	for _, link := range response.Links {
		if link.Rel != "approve" && link.Rel != "payer-action" {
			continue
		}
		u, err := url.Parse(link.Href)
		if err != nil || u.Scheme != "https" || u.User != nil || (u.Host != "www.sandbox.paypal.com" && u.Host != "sandbox.paypal.com") {
			continue
		}
		return PaymentResult{OrderID: response.ID, Status: response.Status, ApprovalURL: link.Href}, nil
	}
	return PaymentResult{}, fmt.Errorf("sandbox returned no valid buyer approval URL; retain the request-id before retrying")
}
func (c *Client) CaptureOrder(ctx context.Context, orderID, requestID string) (PaymentResult, error) {
	if c.config.Environment != "sandbox" {
		return PaymentResult{}, fmt.Errorf("payment test APIs only support sandbox")
	}
	var response order
	if err := c.postJSON(ctx, "/v2/checkout/orders/"+orderID+"/capture", requestID, []byte("{}"), &response); err != nil {
		return PaymentResult{}, err
	}
	if response.ID != orderID || response.Status == "" {
		return PaymentResult{}, fmt.Errorf("sandbox returned an invalid capture order response")
	}
	result := PaymentResult{OrderID: response.ID, Status: response.Status}
	for _, unit := range response.Units {
		for _, capture := range unit.Payments.Captures {
			if !idPattern.MatchString(capture.ID) || capture.Status == "" {
				return PaymentResult{}, fmt.Errorf("sandbox returned invalid capture details")
			}
			result.Captures = append(result.Captures, capture)
		}
	}
	if len(result.Captures) == 0 {
		return PaymentResult{}, fmt.Errorf("sandbox returned no capture details; retain the request-id before retrying")
	}
	return result, nil
}
