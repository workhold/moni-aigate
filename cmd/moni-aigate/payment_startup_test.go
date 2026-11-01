package main

import (
	"context"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/config"
)

func TestPreparePaymentsDoesNotConnectOrdinaryOffAndRejectsInvalidModes(t *testing.T) {
	ctx := context.Background()
	out, err := preparePaymentStartup(ctx, config.Config{PaymentsMode: "off", DatabaseURL: "postgres://unresolvable.invalid/unused"}, "also invalid")
	if err != nil || out.environment || out.applicationPool != nil || out.projectionPool != nil {
		t.Fatal("ordinary off parsed or connected payment targets", err)
	}
	for _, c := range []config.Config{{PaymentsMode: "live"}, {PaymentsMode: "sandbox"}, {PaymentsMode: "off", PaymentsAppManifest: "/private/no-such-app-secret"}, {PaymentsMode: "off", PaymentsAppManifest: "/private/no-such-app-secret", PaymentsWorkerManifest: "/private/no-such-worker-secret"}} {
		_, err = preparePaymentStartup(ctx, c, "postgres://invalid/unused")
		if err == nil || strings.Contains(err.Error(), "/private/") {
			t.Fatal("unsafe or accepted configuration", err)
		}
	}
}
