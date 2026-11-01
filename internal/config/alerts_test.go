package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestOperationsAlertsDefaultOffAndInvalidConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://fixture/unused")
	t.Setenv("MASTER_KEYS_JSON", "")
	t.Setenv("MASTER_KEY_ID", "fixture")
	t.Setenv("MASTER_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	t.Setenv("AUTH_SECRET", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32))))
	for _, value := range []string{"", "false", "true", "enabled-sensitive-canary"} {
		t.Setenv("OPERATIONS_ALERTS_ENABLED", value)
		c, e := Load()
		if value == "enabled-sensitive-canary" {
			if e == nil || e.Error() != "OPERATIONS_ALERTS_ENABLED must be a boolean" {
				t.Fatalf("unsafe configuration failure: %v", e)
			}
		} else if e != nil || c.OperationsAlertsEnabled != (value == "true") {
			t.Fatalf("value=%q enabled=%v error=%v", value, c.OperationsAlertsEnabled, e)
		}
	}
}
