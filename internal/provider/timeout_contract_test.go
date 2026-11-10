package provider

import (
	"testing"
	"time"
)

func TestProviderTimeoutConfigBounds(t *testing.T) {
	defaults := DefaultTimeoutConfig()
	if err := defaults.Validate(); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		set  func(*TimeoutConfig, time.Duration)
		max  time.Duration
	}{
		{"connect", func(c *TimeoutConfig, d time.Duration) { c.ConnectTimeout = d }, MaxConnectTimeout},
		{"header", func(c *TimeoutConfig, d time.Duration) { c.HeaderTimeout = d }, MaxHeaderTimeout},
		{"first-event", func(c *TimeoutConfig, d time.Duration) { c.FirstEventTimeout = d }, MaxFirstEventTimeout},
		{"idle", func(c *TimeoutConfig, d time.Duration) { c.IdleTimeout = d }, MaxIdleTimeout},
		{"progress", func(c *TimeoutConfig, d time.Duration) { c.ProgressTimeout = d }, MaxProgressTimeout},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			low := defaults
			tc.set(&low, MinProviderTimeout-time.Nanosecond)
			if err := low.Validate(); err == nil {
				t.Fatal("below-minimum timeout accepted")
			}
			high := defaults
			tc.set(&high, tc.max+time.Nanosecond)
			if err := high.Validate(); err == nil {
				t.Fatal("above-maximum timeout accepted")
			}
		})
	}
}

func TestNewClientNeverRetainsUnsafeTimeouts(t *testing.T) {
	c := NewClient(ClientOptions{
		ConnectTimeout:    0,
		HeaderTimeout:     MaxHeaderTimeout + time.Second,
		FirstEventTimeout: MinProviderTimeout - time.Nanosecond,
		IdleTimeout:       MaxIdleTimeout + time.Second,
		ProgressTimeout:   -time.Second,
	})
	defaults := DefaultTimeoutConfig()
	if c.options.ConnectTimeout != defaults.ConnectTimeout || c.options.HeaderTimeout != defaults.HeaderTimeout || c.options.FirstEventTimeout != defaults.FirstEventTimeout || c.options.IdleTimeout != defaults.IdleTimeout || c.options.ProgressTimeout != defaults.ProgressTimeout {
		t.Fatalf("unsafe timeout survived normalization: %+v", c.options)
	}
}
