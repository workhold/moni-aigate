package paymentsandboxqa

import (
	"context"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/migrations"
)

func (e *Environment) fund(ctx context.Context) error {
	p, err := e.OpenPool(ctx, "app")
	if err != nil {
		return err
	}
	defer p.Close()
	project, err := e.OpenPool(ctx, "projector")
	if err != nil {
		return err
	}
	defer project.Close()
	if migrations.VerifyCurrent(ctx, p) != nil || operations.VerifyAlertRole(ctx, p, operations.AlertApplicationRole) != nil || operations.VerifyAlertRole(ctx, project, operations.AlertProjectionRole) != nil {
		return stage("actual_application_roles")
	}
	for _, pair := range []struct {
		role       string
		projection bool
	}{{"app_runtime", false}, {"app_projector", true}} {
		pool := p
		if pair.projection {
			pool = project
		}
		var current, session string
		if pool.QueryRow(ctx, "SELECT current_user,session_user").Scan(&current, &session) != nil || current != e.Roles[pair.role] || session != current {
			return stage("actual_application_login")
		}
	}
	tenant, maker, checker := uuid.NewString(), uuid.NewString(), uuid.NewString()
	e.TenantID = tenant
	if _, err = p.Exec(ctx, "INSERT INTO tenants(id,name) VALUES($1,'022 isolated APP funding fixture')", tenant); err != nil {
		return stage("tenant")
	}
	for _, u := range []struct{ id, role string }{{maker, "finance_operator"}, {checker, "finance_approver"}} {
		if _, err = p.Exec(ctx, "INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture-only',$3)", u.id, u.id+"@payment-qa.invalid", u.role); err != nil {
			return stage("financial_users")
		}
	}
	s := billing.New(p)
	in := billing.CreditInput{TenantID: tenant, ActorID: maker, Amount: 1000, BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: 1000, BusinessRef: "bank:qa:" + uuid.NewString(), ProofRef: "proof:qa:verified", IdempotencyKey: uuid.NewString(), RequestHMAC: uuid.NewString()}
	order, err := s.CreateCredit(ctx, in)
	if err != nil {
		return stage("credit")
	}
	approve := billing.ApprovalInput{TenantID: tenant, ActorID: checker, OrderID: order.ID}
	done, err := s.ApproveCredit(ctx, approve)
	if err != nil {
		return stage("approval")
	}
	if done.State != "approved" || done.Requester != maker || done.Approver != checker || done.JournalID == "" {
		return stage("dual_approval")
	}
	repeated, err := s.CreateCredit(ctx, in)
	if err != nil || repeated.ID != done.ID {
		return stage("credit_replay")
	}
	again, err := s.ApproveCredit(ctx, approve)
	if err != nil || again.JournalID != done.JournalID {
		return stage("approval_replay")
	}
	if _, err = billing.New(project).Project(ctx, billing.ProjectInput{}); err != nil {
		return stage("projection")
	}
	funding, err := s.VerifyFunding(ctx, tenant)
	if err != nil || !funding.Balanced {
		return stage("funding")
	}
	var sources, journals, receipts, outbox, lines, projected int
	var available int64
	err = p.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM credit_orders WHERE id=$1 AND state='approved' AND requester=$3 AND approver=$4),
 (SELECT count(*) FROM journals WHERE operation_id=$1 AND id=$2 AND kind='credit'),
 (SELECT count(*) FROM funding_receipts WHERE source_kind='manual_credit' AND source_id=$1 AND journal_id=$2 AND amount=1000),
 (SELECT count(*) FROM projection_outbox WHERE operation_id=$1 AND kind='credited' AND schema_version=1 AND sanitized_payload='{"amount":"1000"}'::jsonb),
 (SELECT count(*) FROM journal_lines WHERE journal_id=$2),
 (SELECT count(*) FROM projection_receipts),
 (SELECT balance FROM ledger_accounts WHERE tenant_id=$5 AND kind='AVAILABLE')`, done.ID, done.JournalID, maker, checker, tenant).Scan(&sources, &journals, &receipts, &outbox, &lines, &projected, &available)
	if err != nil || sources != 1 || journals != 1 || receipts != 1 || outbox != 1 || lines != 2 || projected < 1 || available != 1000 {
		return stage("unique_funding")
	}
	e.fundingDigest, err = e.FundingSnapshot(ctx)
	return err
}

const fundingSnapshotSQL = `SELECT encode(sha256(convert_to(jsonb_build_object(
 'journals',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM journals x),'[]'::jsonb),
 'journal_lines',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM journal_lines x),'[]'::jsonb),
 'receipts',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM funding_receipts x),'[]'::jsonb),
 'sources',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM credit_orders x),'[]'::jsonb),
 'accounts',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM ledger_accounts x),'[]'::jsonb),
 'lots',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM funding_lots x),'[]'::jsonb),
 'lot_balances',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.lot_id) FROM funding_lot_balances x),'[]'::jsonb),
 'lot_events',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM funding_lot_events x),'[]'::jsonb),
 'outbox',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.event_id) FROM projection_outbox x),'[]'::jsonb),
 'idempotency',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.tenant_id,x.operation,x.key) FROM financial_idempotency x),'[]'::jsonb),
 'audit',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM audit_events x),'[]'::jsonb))::text,'UTF8')),'hex')`

func (e *Environment) FundingSnapshot(ctx context.Context) (string, error) {
	p, err := e.OpenPool(ctx, "app")
	if err != nil {
		return "", err
	}
	defer p.Close()
	var hash string
	if p.QueryRow(ctx, fundingSnapshotSQL).Scan(&hash) != nil {
		return "", stage("funding_snapshot")
	}
	return hash, nil
}
func (e *Environment) VerifyFundsUnchanged(ctx context.Context) error {
	hash, err := e.FundingSnapshot(ctx)
	if err != nil {
		return err
	}
	if e.fundingDigest == "" || hash != e.fundingDigest {
		return stage("funding_changed")
	}
	return nil
}
