package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"paypal/server/internal/paypal"
	"regexp"
	"strings"
	"time"
)

func main() { os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)) }

func run(ctx context.Context, args []string, out, errs io.Writer) int {
	if len(args) == 0 || (args[0] != "create" && args[0] != "capture") {
		fmt.Fprintln(errs, "usage: paypal-payment create|capture [flags]")
		return 1
	}
	action := args[0]
	flags := flag.NewFlagSet("paypal-payment "+action, flag.ContinueOnError)
	flags.SetOutput(errs)
	path := flags.String("config", "configs/paypal.private.json", "sandbox seller application credentials")
	requestID := flags.String("request-id", "", "unique idempotency key; reuse it only when retrying the same operation")
	var amount, currency, returnURL, cancelURL, orderID string
	if action == "create" {
		flags.StringVar(&amount, "amount", "", "positive exact decimal amount")
		flags.StringVar(&currency, "currency", "USD", "three-letter currency code")
		flags.StringVar(&returnURL, "return-url", "http://localhost:8080/ping", "URL after buyer approval")
		flags.StringVar(&cancelURL, "cancel-url", "http://localhost:8080/ping", "URL after buyer cancellation")
	} else {
		flags.StringVar(&orderID, "order-id", "", "approved sandbox order ID")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return 1
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(errs, "unexpected positional arguments")
		return 1
	}
	if !requestPattern.MatchString(*requestID) {
		fmt.Fprintln(errs, "request-id must contain 1–38 letters, digits, dots, underscores, colons or hyphens")
		return 1
	}
	if action == "create" {
		if err := validatePayment(amount, currency, returnURL, cancelURL); err != nil {
			fmt.Fprintln(errs, err)
			return 1
		}
	} else if !idPattern.MatchString(orderID) {
		fmt.Fprintln(errs, "order-id must be alphanumeric")
		return 1
	}
	c, err := paypal.LoadSandboxCredentials(*path)
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	api := paypal.NewClient(c, &http.Client{Timeout: 30 * time.Second})
	if err := api.Authenticate(ctx); err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	var result paypal.PaymentResult
	if action == "create" {
		result, err = api.CreateOrder(ctx, amount, currency, returnURL, cancelURL, *requestID)
	} else {
		result, err = api.CaptureOrder(ctx, orderID, *requestID)
	}
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(errs, "cannot write payment result; retry only with the same request-id and arguments")
		return 1
	}
	return 0
}

var requestPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,38}$`)
var idPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)
var amountPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})(\.[0-9]{1,2})?$`)
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

func validatePayment(amount, currency, returnURL, cancelURL string) error {
	if !amountPattern.MatchString(amount) || strings.Trim(amount, "0.") == "" {
		return fmt.Errorf("amount must be positive, with at most nine integer digits and two decimal places")
	}
	if !currencyPattern.MatchString(currency) {
		return fmt.Errorf("currency must be three uppercase letters supported by PayPal")
	}
	if (currency == "JPY" || currency == "HUF" || currency == "TWD") && strings.Contains(amount, ".") {
		return fmt.Errorf("this currency requires an integer amount")
	}
	for _, value := range []string{returnURL, cancelURL} {
		if !validCallback(value) {
			return fmt.Errorf("return-url and cancel-url must be HTTPS URLs or local HTTP URLs without credentials")
		}
	}
	return nil
}
