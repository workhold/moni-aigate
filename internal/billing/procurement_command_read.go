//go:build historical

package billing

import (
	"context"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// readProcurementCommandPages shares the original scan's budget and query.
// Audit/outbox rows are read in full before their scoped cores select actions.
func readProcurementCommandPages(ctx context.Context, query fundingSourceQuery, schema string, commands *fundinglotverify.ProcurementCommandRows, journal *fundinglotverify.ProcurementCostJournalRows, budget *fundingSourceReadBudget) error {
	if err := readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "procurement_commands", "operation_id", "operation_id", "actor", "kind", "target_id", "input_digest", "status", "reason", "before_state::pg_catalog.text", "after_state::pg_catalog.text", "created_at"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementCommandRow
		if err := source.Scan(&row.OperationID, &row.Actor, &row.Kind, &row.TargetID, &row.InputDigest, &row.Status, &row.Reason, &row.Before, &row.After, &row.CreatedAt); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		return row.OperationID, []string{row.OperationID, row.Actor, row.Kind, row.TargetID, row.InputDigest, row.Status, row.Reason, row.Before, row.After}, func() { commands.Commands = append(commands.Commands, row) }, nil
	}, budget); err != nil {
		return err
	}
	if err := readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "procurement_balance_observations", "id", "id", "account_id", "operation_id", "actor", "status", "amount::pg_catalog.text", "currency", "source", "semantic", "observed_at", "provider_as_of", "evidence_ref", "created_at"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementObservationRow
		if err := source.Scan(&row.ID, &row.AccountID, &row.OperationID, &row.Actor, &row.Status, &row.Amount, &row.Currency, &row.Source, &row.Semantic, &row.ObservedAt, &row.ProviderAsOf, &row.EvidenceRef, &row.CreatedAt); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		fields := procurementAttemptTextFields([]string{row.ID, row.AccountID, row.OperationID, row.Actor, row.Status, row.Currency, row.Source, row.Semantic, row.EvidenceRef}, row.Amount)
		return row.ID, fields, func() { commands.Observations = append(commands.Observations, row) }, nil
	}, budget); err != nil {
		return err
	}
	if err := readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "audit_events", "id", "id", "actor", "tenant_scope", "action", "target", "sanitized_diff::pg_catalog.text", "request_id", "created_at"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementAuditRow
		if err := source.Scan(&row.ID, &row.Actor, &row.TenantScope, &row.Action, &row.Target, &row.Diff, &row.RequestID, &row.CreatedAt); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		fields := procurementAttemptTextFields([]string{row.ID, row.Actor, row.Action, row.Target, row.Diff, row.RequestID}, row.TenantScope)
		return row.ID, fields, func() { commands.Audits = append(commands.Audits, row) }, nil
	}, budget); err != nil {
		return err
	}
	return readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "projection_outbox", "event_id", "event_id", "tenant_id", "operation_id", "kind", "schema_version", "sanitized_payload::pg_catalog.text", "created_at"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementOutboxRow
		if err := source.Scan(&row.ID, &row.TenantID, &row.OperationID, &row.Kind, &row.SchemaVersion, &row.Payload, &row.CreatedAt); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		return row.ID, []string{row.ID, row.TenantID, row.OperationID, row.Kind, row.Payload}, func() { journal.Outbox = append(journal.Outbox, row) }, nil
	}, budget)
}
