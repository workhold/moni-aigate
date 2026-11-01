package paymentsandboxqa

import "context"

// Only the retained A tenant and manual clearing graph belong to this witness.
// Projection receipts and reports may advance while the independent B tenant pays.
const applicationLegacyFundingSQL = `SELECT encode(sha256(convert_to(jsonb_build_object(
 'journals',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM journals x WHERE x.tenant_id=$1),'[]'::jsonb),
 'journal_lines',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM journal_lines x JOIN journals j ON j.id=x.journal_id WHERE j.tenant_id=$1),'[]'::jsonb),
 'receipts',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM funding_receipts x WHERE x.tenant_id=$1),'[]'::jsonb),
 'sources',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM credit_orders x WHERE x.tenant_id=$1),'[]'::jsonb),
 'accounts',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM ledger_accounts x WHERE x.tenant_id=$1 OR (x.tenant_id IS NULL AND x.kind='FUNDING_CLEARING')),'[]'::jsonb),
 'outbox',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.event_id) FROM projection_outbox x WHERE x.tenant_id=$1),'[]'::jsonb),
 'idempotency',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.tenant_id,x.operation,x.key) FROM financial_idempotency x WHERE x.tenant_id=$1),'[]'::jsonb),
 'audit',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM audit_events x WHERE x.tenant_scope=$1 OR x.target IN (SELECT c.id FROM credit_orders c WHERE c.tenant_id=$1)),'[]'::jsonb))::text,'UTF8')),'hex')`

func (e *ApplicationEnvironment) LegacyFundingSnapshot(ctx context.Context) (string, error) {
	if e == nil || e.Environment == nil || e.TenantID == "" {
		return "", ErrEnvironment
	}
	p, err := e.OpenPool(ctx, "app")
	if err != nil {
		return "", err
	}
	defer p.Close()
	var hash string
	if p.QueryRow(ctx, applicationLegacyFundingSQL, e.TenantID).Scan(&hash) != nil {
		return "", stage("application_legacy_funding_snapshot")
	}
	return hash, nil
}

func (e *ApplicationEnvironment) VerifyLegacyFundingUnchanged(ctx context.Context) error {
	hash, err := e.LegacyFundingSnapshot(ctx)
	if err != nil {
		return err
	}
	if e.legacyDigest == "" || hash != e.legacyDigest {
		return stage("application_legacy_funding_changed")
	}
	return nil
}
