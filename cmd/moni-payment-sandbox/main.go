// moni-payment-sandbox runs exactly one already-provisioned local QA component.
// It has no init, migration, default-state or application gateway mode.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moni-ai/moni-aigate/internal/mockpsp"
	"github.com/moni-ai/moni-aigate/internal/paymentreceiver"
)

type componentServer interface {
	Run(context.Context) error
	Close(context.Context) error
}

func run(args []string) error {
	if len(args) == 0 || args[0] != "serve" {
		return fmt.Errorf("sandbox_usage_invalid")
	}
	f := flag.NewFlagSet("serve", flag.ContinueOnError)
	f.SetOutput(discard{})
	component := f.String("component", "", "mock or receiver")
	path := f.String("manifest-file", "", "existing private component manifest")
	if f.Parse(args[1:]) != nil || f.NArg() != 0 || *path == "" || (*component != "mock" && *component != "receiver") {
		return fmt.Errorf("sandbox_usage_invalid")
	}
	life, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	startup, stop := context.WithTimeout(life, 10*time.Second)
	var s componentServer
	var err error
	if *component == "mock" {
		var cfg mockpsp.RuntimeConfig
		cfg, err = mockpsp.LoadRuntimeConfig(startup, *path)
		if err == nil {
			s, err = mockpsp.New(startup, cfg)
		}
	} else {
		var cfg paymentreceiver.RuntimeConfig
		cfg, err = paymentreceiver.LoadRuntimeConfig(startup, *path)
		if err == nil {
			s, err = paymentreceiver.New(startup, cfg)
		}
	}
	stop()
	if err != nil {
		return fmt.Errorf("sandbox_startup_refused")
	}
	defer func() {
		cleanup, c := context.WithTimeout(context.Background(), 15*time.Second)
		defer c()
		_ = s.Close(cleanup)
	}()
	return s.Run(life)
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
func main() {
	if run(os.Args[1:]) != nil {
		fmt.Fprintln(os.Stderr, "sandbox_operation_failed")
		os.Exit(1)
	}
}
