package maintenancetrust_test

import (
	"context"
	"maps"

	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxqa"
)

func init() {
	maintenancetrust.RegisterSandboxQAForTest(func(ctx context.Context, dsn, root string) (*maintenancetrust.SandboxQAEnvironmentForTest, error) {
		env, err := paymentsandboxqa.CreateApplication(ctx, dsn, root)
		if err != nil {
			return nil, err
		}
		return &maintenancetrust.SandboxQAEnvironmentForTest{
			Roles: maps.Clone(env.Roles), Schemas: maps.Clone(env.Schemas),
			OpenPool: env.OpenPool, Close: env.Close,
			VerifyLegacyFundingUnchanged: env.VerifyLegacyFundingUnchanged,
		}, nil
	})
}
