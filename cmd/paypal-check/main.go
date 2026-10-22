package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() { os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)) }

func run(ctx context.Context, args []string, out, errs io.Writer) int {
	flags := flag.NewFlagSet("paypal-check", flag.ContinueOnError)
	flags.SetOutput(errs)
	path := flags.String("config", "configs/paypal.private.json", "PayPal integration-check configuration")
	/* Temporarily disabled: identity mappings and subsequent checks.
	samplesPath := flags.String("samples", "", "optional same-environment capture/refund/webhook sample JSON")
	*/
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(errs, "unexpected positional arguments")
		return 1
	}
	c, err := LoadConfig(*path)
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	/* Temporarily disabled: identity mappings and subsequent checks.
	samples, err := LoadSamples(*samplesPath)
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	*/
	report, err := Run(ctx, c, nil)
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(errs, "cannot write check report")
		return 1
	}
	if report.HasFailures() {
		return 1
	}
	return 0
}
