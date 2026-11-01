package main

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/config"
	"github.com/moni-ai/moni-aigate/internal/payments"
)

type paymentStartup struct {
	application, worker             payments.Options
	applicationPool, projectionPool *pgxpool.Config
	environment                     bool
}

// All three sandbox connection paths are validated before the first connection.
// Maintenance commands use their existing offline owner path instead.
func preparePaymentStartup(ctx context.Context, c config.Config, projectionDSN string) (paymentStartup, error) {
	out := paymentStartup{application: payments.DisabledOptions()}
	if c.PaymentsMode != "" && c.PaymentsMode != "off" && c.PaymentsMode != "sandbox" {
		return out, errors.New("payment mode unavailable")
	}
	if (c.PaymentsAppManifest == "") != (c.PaymentsWorkerManifest == "") {
		return out, errors.New("payment manifests incomplete")
	}
	if c.PaymentsAppManifest == "" {
		if c.PaymentsMode == "sandbox" {
			return out, errors.New("payment manifests required")
		}
		return out, nil
	}
	var err error
	out.application, err = payments.LoadSandboxOptions(ctx, c.PaymentsAppManifest)
	if err != nil {
		return out, errors.New("payment application configuration invalid")
	}
	out.worker, err = payments.LoadWorkerOptions(ctx, c.PaymentsWorkerManifest)
	if err != nil {
		return out, errors.New("payment worker configuration invalid")
	}
	if payments.ValidateOptionsPair(out.application, out.worker) != nil || payments.ValidateApplicationAddress(out.application, c.Addr) != nil {
		return out, errors.New("payment environment configuration invalid")
	}
	out.applicationPool, err = payments.ApplicationPoolConfig(ctx, c.DatabaseURL, out.application)
	if err != nil {
		return out, errors.New("payment application database configuration invalid")
	}
	out.projectionPool, err = payments.ProjectionPoolConfig(ctx, projectionDSN, out.application)
	if err != nil {
		return out, errors.New("payment projection database configuration invalid")
	}
	out.environment = true
	return out, nil
}
