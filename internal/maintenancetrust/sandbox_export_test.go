package maintenancetrust

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SandboxQAEnvironmentForTest is test assembly only. It contains neither
// maintenance authority nor a substitute for the genuine QA installer. The
// external test package supplies the original installer's exact methods so
// internal tests retain their private authority/catalog/funding oracles without
// an internal-test -> paymentsandboxqa -> billing -> maintenancetrust cycle.
// This file is absent from production builds.
type SandboxQAEnvironmentForTest struct {
	Roles                        map[string]string
	Schemas                      map[string]string
	ApplicationManifestPath      string
	OpenPool                     func(context.Context, string) (*pgxpool.Pool, error)
	Close                        func(context.Context) error
	VerifyLegacyFundingUnchanged func(context.Context) error
}

var sandboxQAFactoryForTest func(context.Context, string, string) (*SandboxQAEnvironmentForTest, error)

// RegisterSandboxQAForTest is called once during external test initialization.
// It exposes no production registration or proof/permit construction facility.
func RegisterSandboxQAForTest(factory func(context.Context, string, string) (*SandboxQAEnvironmentForTest, error)) {
	if factory == nil || sandboxQAFactoryForTest != nil {
		panic("sandbox QA test factory registration")
	}
	sandboxQAFactoryForTest = factory
}
