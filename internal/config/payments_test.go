package config

import "testing"

func TestPaymentsModeFailsClosedBeforeConnection(t *testing.T) {
	for _, tc := range []struct {
		mode, app, worker string
		valid             bool
	}{
		{"", "", "", true}, {"off", "", "", true},
		{"off", "/private/app", "/private/worker", true},
		{"sandbox", "/private/app", "/private/worker", true},
		{"sandbox", "", "", false}, {"off", "/private/app", "", false},
		{"off", "", "/private/worker", false}, {"live", "", "", false},
		{"SANDBOX", "", "", false}, {" off", "", "", false},
	} {
		t.Run(tc.mode+tc.app+tc.worker, func(t *testing.T) {
			t.Setenv("PAYMENTS_MODE", tc.mode)
			t.Setenv("PAYMENTS_APP_MANIFEST", tc.app)
			t.Setenv("PAYMENTS_WORKER_MANIFEST", tc.worker)
			var c Config
			if err := loadPayments(&c); (err == nil) != tc.valid {
				t.Fatal("mode validation", err)
			}
			if tc.valid && tc.mode == "" && c.PaymentsMode != "off" {
				t.Fatal("default must be off")
			}
		})
	}
}
