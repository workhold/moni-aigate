package config

import (
	"errors"
	"os"
)

// Payment mode validation runs before any database connection. Private file
// and sandbox target validation belong to the payment runtime constructor.
func loadPayments(c *Config) error {
	c.PaymentsMode = env("PAYMENTS_MODE", "off")
	c.PaymentsAppManifest = os.Getenv("PAYMENTS_APP_MANIFEST")
	c.PaymentsWorkerManifest = os.Getenv("PAYMENTS_WORKER_MANIFEST")
	if c.PaymentsMode != "off" && c.PaymentsMode != "sandbox" {
		return errors.New("PAYMENTS_MODE must be off or sandbox; live payments are unavailable")
	}
	app, worker := c.PaymentsAppManifest != "", c.PaymentsWorkerManifest != ""
	if app != worker || c.PaymentsMode == "sandbox" && !app {
		return errors.New("payment sandbox requires both private manifests")
	}
	return nil
}
