package paymentsandboxqa

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/security"
)

func (e *ApplicationEnvironment) seedApplicationUsers(ctx context.Context) error {
	p, err := e.OpenPool(ctx, "app")
	if err != nil {
		return err
	}
	defer p.Close()
	tx, err := p.Begin(ctx)
	if err != nil {
		return stage("application_users_transaction")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	tenant := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO tenants(id,name) VALUES($1,'022 isolated APP payment QA')`, tenant); err != nil {
		return stage("application_tenant")
	}
	credentials := map[string]applicationCredential{}
	actors := map[string]string{}
	for _, actor := range []struct{ name, platform, membership string }{
		{"admin", "admin", ""}, {"owner", "none", "owner"}, {"billing_viewer", "none", "billing_viewer"}, {"member", "none", "member"}, {"finance_operator", "finance_operator", ""}, {"finance_approver", "finance_approver", ""},
	} {
		id := uuid.NewString()
		password := encoded(fresh())
		email := id + "@application-payment-qa.invalid"
		hash, err := security.HashPassword(password)
		if err != nil {
			return stage("application_password")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,$3,$4)`, id, email, hash, actor.platform); err != nil {
			return stage("application_users")
		}
		if actor.membership != "" {
			if _, err = tx.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,$3)`, tenant, id, actor.membership); err != nil {
				return stage("application_memberships")
			}
		}
		actors[actor.name] = id
		credentials[actor.name] = applicationCredential{email: email, password: password}
	}
	if tx.Commit(ctx) != nil {
		return stage("application_users_commit")
	}
	e.appMu.Lock()
	defer e.appMu.Unlock()
	e.ApplicationTenantID = tenant
	e.ActorIDs = actors
	e.credentials = credentials
	return nil
}
