package billing

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// procurementAttemptPageSQL builds one of the fixed single-column C-order
// keyset readers used by the bounded ScanProcurement projection.
func procurementAttemptPageSQL(schema, table, cursorColumn string, columns ...string) string {
	identifier := pgx.Identifier{schema, table}.Sanitize()
	cursor := pgx.Identifier{cursorColumn}.Sanitize()
	return "SELECT " + strings.Join(columns, ",") + " FROM " + identifier +
		" WHERE NOT $1::pg_catalog.bool OR " + cursor + " COLLATE \"C\" > $2::pg_catalog.text COLLATE \"C\"" +
		" ORDER BY " + cursor + " COLLATE \"C\" LIMIT 128"
}

func procurementAttemptPoolPageSQL(schema string) string {
	identifier := pgx.Identifier{schema, "channel_version_credentials"}.Sanitize()
	return "SELECT channel_version_id,channel_id,credential_id,position,weight,max_inflight FROM " + identifier +
		" WHERE NOT $1::pg_catalog.bool OR (channel_version_id COLLATE \"C\", credential_id COLLATE \"C\") > " +
		"($2::pg_catalog.text COLLATE \"C\", $3::pg_catalog.text COLLATE \"C\")" +
		" ORDER BY channel_version_id COLLATE \"C\", credential_id COLLATE \"C\" LIMIT 128"
}

// readProcurementAttemptPoolPages handles the one composite keyset. Cursor
// components remain separate and are compared with byte ordering, matching
// PostgreSQL's explicit C collation.
func readProcurementAttemptPoolPages(
	ctx context.Context,
	query fundingSourceQuery,
	sql string,
	scan func(fundingSourceRows) (string, string, []string, func(), error),
	budget *fundingSourceReadBudget,
) error {
	hasCursor, lastVersion, lastCredential := false, "", ""
	for {
		if ctx.Err() != nil {
			return fundingSourceReadFailure(ctx)
		}
		rows, err := query.Query(sql, hasCursor, lastVersion, lastCredential)
		if err != nil {
			return fundingSourceReadFailure(ctx)
		}
		count := 0
		pageLastVersion, pageLastCredential := lastVersion, lastCredential
		for {
			if ctx.Err() != nil {
				_ = rows.Close()
				return fundingSourceReadFailure(ctx)
			}
			more, nextErr := rows.Next()
			if nextErr != nil {
				_ = rows.Close()
				return fundingSourceReadFailure(ctx)
			}
			if !more {
				break
			}
			version, credential, fields, retain, scanErr := scan(rows)
			if scanErr != nil {
				_ = rows.Close()
				return fundingSourceReadFailure(ctx)
			}
			if count >= fundingSourcePageSize || (hasCursor || count > 0) && !procurementAttemptTupleGreater(version, credential, pageLastVersion, pageLastCredential) {
				_ = rows.Close()
				return fundingSourceReadExecution
			}
			if err := budget.add(fields...); err != nil {
				_ = rows.Close()
				return err
			}
			if retain == nil {
				_ = rows.Close()
				return fundingSourceReadExecution
			}
			retain()
			pageLastVersion, pageLastCredential = version, credential
			count++
		}
		rowsErr := rows.Err()
		closeErr := rows.Close()
		if ctx.Err() != nil {
			return fundingSourceReadFailure(ctx)
		}
		if rowsErr != nil || closeErr != nil {
			return fundingSourceReadFailure(ctx)
		}
		if count < fundingSourcePageSize {
			if ctx.Err() != nil {
				return fundingSourceReadFailure(ctx)
			}
			return nil
		}
		hasCursor, lastVersion, lastCredential = true, pageLastVersion, pageLastCredential
	}
}

func procurementAttemptTupleGreater(version, credential, priorVersion, priorCredential string) bool {
	return version > priorVersion || version == priorVersion && credential > priorCredential
}

func procurementAttemptTextFields(required []string, nullable ...*string) []string {
	fields := append([]string(nil), required...)
	for _, value := range nullable {
		if value != nil {
			fields = append(fields, *value)
		}
	}
	return fields
}

func readProcurementAttemptBindingFacts(
	ctx context.Context,
	query fundingSourceQuery,
	schema string,
) (ProcurementAttemptBindingFacts, error) {
	if ctx == nil {
		return ProcurementAttemptBindingFacts{}, fundingSourceReadInput
	}
	if ctx.Err() != nil {
		return ProcurementAttemptBindingFacts{}, fundingSourceReadFailure(ctx)
	}
	if query == nil || !validProcurementBudgetSchema(schema) {
		return ProcurementAttemptBindingFacts{}, fundingSourceReadInput
	}

	rows := fundinglotverify.ProcurementAttemptBindingRows{}
	approvalRows := fundinglotverify.ProcurementCostApprovalRows{}
	var budget fundingSourceReadBudget

	if err := readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "channels", "id", "id"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementAttemptChannelRow
		if err := source.Scan(&row.ID); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		return row.ID, []string{row.ID}, func() { rows.Channels = append(rows.Channels, row) }, nil
	}, &budget); err != nil {
		return ProcurementAttemptBindingFacts{}, err
	}
	if err := readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "channel_credentials", "id", "id", "channel_id"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementAttemptCredentialRow
		if err := source.Scan(&row.ID, &row.ChannelID); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		return row.ID, []string{row.ID, row.ChannelID}, func() { rows.Credentials = append(rows.Credentials, row) }, nil
	}, &budget); err != nil {
		return ProcurementAttemptBindingFacts{}, err
	}
	if err := readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "channel_versions", "id", "id", "channel_id", "credential_id"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementAttemptVersionRow
		if err := source.Scan(&row.ID, &row.ChannelID, &row.CredentialID); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		return row.ID, []string{row.ID, row.ChannelID, row.CredentialID}, func() { rows.Versions = append(rows.Versions, row) }, nil
	}, &budget); err != nil {
		return ProcurementAttemptBindingFacts{}, err
	}
	if err := readProcurementAttemptPoolPages(ctx, query, procurementAttemptPoolPageSQL(schema), func(source fundingSourceRows) (string, string, []string, func(), error) {
		var row fundinglotverify.ProcurementAttemptPoolMemberRow
		if err := source.Scan(&row.ChannelVersionID, &row.ChannelID, &row.CredentialID, &row.Position, &row.Weight, &row.MaxInflight); err != nil {
			return "", "", nil, nil, fundingSourceReadExecution
		}
		return row.ChannelVersionID, row.CredentialID,
			[]string{row.ChannelVersionID, row.ChannelID, row.CredentialID},
			func() { rows.PoolMemberships = append(rows.PoolMemberships, row) }, nil
	}, &budget); err != nil {
		return ProcurementAttemptBindingFacts{}, err
	}
	if err := readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "tenants", "id", "id"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementAttemptTenantRow
		if err := source.Scan(&row.ID); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		return row.ID, []string{row.ID}, func() { rows.Tenants = append(rows.Tenants, row) }, nil
	}, &budget); err != nil {
		return ProcurementAttemptBindingFacts{}, err
	}
	if err := readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "request_records", "id", "id", "tenant_id", "input_upper_bound::pg_catalog.text", "output_limit::pg_catalog.text"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementAttemptRequestRow
		if err := source.Scan(&row.ID, &row.TenantID, &row.InputUpperBound, &row.OutputLimit); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		return row.ID, []string{row.ID, row.TenantID, row.InputUpperBound, row.OutputLimit}, func() { rows.Requests = append(rows.Requests, row) }, nil
	}, &budget); err != nil {
		return ProcurementAttemptBindingFacts{}, err
	}
	if err := readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "cost_versions", "id", "id", "channel_id", "upstream_model", "input_rate::pg_catalog.text", "output_rate::pg_catalog.text", "basis", "effective_at", "created_at"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementAttemptCostVersionRow
		if err := source.Scan(&row.ID, &row.ChannelID, &row.UpstreamModel, &row.InputRate, &row.OutputRate, &row.Basis, &row.EffectiveAt, &row.CreatedAt); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		fields := procurementAttemptTextFields([]string{row.ID, row.ChannelID, row.UpstreamModel, row.Basis}, row.InputRate, row.OutputRate)
		return row.ID, fields, func() { rows.CostVersions = append(rows.CostVersions, row) }, nil
	}, &budget); err != nil {
		return ProcurementAttemptBindingFacts{}, err
	}
	if err := readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "procurement_accounts", "id", "id", "held_amount::pg_catalog.text", "spent_amount::pg_catalog.text"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementBudgetAccountRow
		if err := source.Scan(&row.ID, &row.HeldAmount, &row.SpentAmount); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		return row.ID, []string{row.ID, row.HeldAmount, row.SpentAmount}, func() { rows.Accounts = append(rows.Accounts, row) }, nil
	}, &budget); err != nil {
		return ProcurementAttemptBindingFacts{}, err
	}
	if err := readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "credential_procurement_bindings", "id", "id", "credential_id", "channel_id", "account_id", "operation_id", "bound_at"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementAttemptBindingRow
		if err := source.Scan(&row.ID, &row.CredentialID, &row.ChannelID, &row.AccountID, &row.OperationID, &row.BoundAt); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		return row.ID, []string{row.ID, row.CredentialID, row.ChannelID, row.AccountID, row.OperationID}, func() { rows.Bindings = append(rows.Bindings, row) }, nil
	}, &budget); err != nil {
		return ProcurementAttemptBindingFacts{}, err
	}
	if err := readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "upstream_attempts", "id", "id", "tenant_id", "request_id", "sequence", "channel_id", "channel_version_id", "credential_id", "upstream_model", "delivery_state", "cost_version_id", "cost_input_rate::pg_catalog.text", "cost_output_rate::pg_catalog.text", "cost_basis", "cost_status", "cost_amount::pg_catalog.text", "created_at"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementAttemptRow
		if err := source.Scan(&row.ID, &row.TenantID, &row.RequestID, &row.Sequence, &row.ChannelID, &row.ChannelVersionID, &row.CredentialID, &row.UpstreamModel, &row.DeliveryState, &row.CostVersionID, &row.CostInputRate, &row.CostOutputRate, &row.CostBasis, &row.CostStatus, &row.CostAmount, &row.CreatedAt); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		fields := procurementAttemptTextFields([]string{row.ID, row.TenantID, row.RequestID, row.ChannelID, row.ChannelVersionID, row.CredentialID, row.UpstreamModel, row.DeliveryState, row.CostBasis, row.CostStatus}, row.CostVersionID, row.CostInputRate, row.CostOutputRate, row.CostAmount)
		return row.ID, fields, func() { rows.Attempts = append(rows.Attempts, row) }, nil
	}, &budget); err != nil {
		return ProcurementAttemptBindingFacts{}, err
	}
	var budgetReservations []fundinglotverify.ProcurementBudgetReservationRow
	if err := readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "procurement_reservations", "attempt_id", "attempt_id", "account_id", "binding_id", "input_bound::pg_catalog.text", "output_bound::pg_catalog.text", "upper_amount::pg_catalog.text", "held_amount::pg_catalog.text", "spent_amount::pg_catalog.text", "state", "anomaly"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementAttemptReservationRow
		if err := source.Scan(&row.AttemptID, &row.AccountID, &row.BindingID, &row.InputBound, &row.OutputBound, &row.UpperAmount, &row.HeldAmount, &row.SpentAmount, &row.State, &row.Anomaly); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		fields := []string{row.AttemptID, row.AccountID, row.BindingID, row.InputBound, row.OutputBound, row.UpperAmount, row.HeldAmount, row.SpentAmount, row.State}
		return row.AttemptID, fields, func() {
			rows.Reservations = append(rows.Reservations, row)
			budgetReservations = append(budgetReservations, fundinglotverify.ProcurementBudgetReservationRow{
				AttemptID: row.AttemptID, AccountID: row.AccountID, UpperAmount: row.UpperAmount,
				HeldAmount: row.HeldAmount, SpentAmount: row.SpentAmount, State: row.State, Anomaly: row.Anomaly,
			})
		}, nil
	}, &budget); err != nil {
		return ProcurementAttemptBindingFacts{}, err
	}
	if err := readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "procurement_budget_events", "id", "id", "event_key", "account_id", "attempt_id", "held_delta::pg_catalog.text", "spent_delta::pg_catalog.text", "type", "evidence_ref"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementBudgetEventRow
		var kind, evidence string
		if err := source.Scan(&row.ID, &row.EventKey, &row.AccountID, &row.AttemptID, &row.HeldDelta, &row.SpentDelta, &kind, &evidence); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		return row.ID, []string{row.ID, row.EventKey, row.AccountID, row.AttemptID, row.HeldDelta, row.SpentDelta, kind, evidence}, func() {
			rows.BudgetEvents = append(rows.BudgetEvents, row)
			approvalRows.Events = append(approvalRows.Events, fundinglotverify.ProcurementApprovalEventRow{
				ID: row.ID, EventKey: row.EventKey, AccountID: row.AccountID, AttemptID: row.AttemptID, Type: kind, EvidenceRef: evidence,
			})
		}, nil
	}, &budget); err != nil {
		return ProcurementAttemptBindingFacts{}, err
	}

	if err := readProcurementApprovalPages(ctx, query, schema, &approvalRows, &budget); err != nil {
		return ProcurementAttemptBindingFacts{}, err
	}
	approvalRows.Attempts = rows.Attempts
	approvalRows.Reservations = rows.Reservations

	budgetCheck, err := fundinglotverify.CheckProcurementBudgetProjection(ctx, fundinglotverify.ProcurementBudgetRows{
		Accounts: rows.Accounts, Reservations: budgetReservations, Events: rows.BudgetEvents,
	})
	if err != nil {
		return ProcurementAttemptBindingFacts{}, procurementAttemptCoreFailure(err)
	}
	attemptCheck, err := fundinglotverify.CheckProcurementAttemptBindingGraph(ctx, rows)
	if err != nil {
		return ProcurementAttemptBindingFacts{}, procurementAttemptCoreFailure(err)
	}
	approvalCheck, err := fundinglotverify.CheckProcurementCostApprovalGraph(ctx, approvalRows)
	if err != nil {
		return ProcurementAttemptBindingFacts{}, procurementAttemptCoreFailure(err)
	}
	return ProcurementAttemptBindingFacts{budget: budgetCheck, binding: attemptCheck, approval: approvalCheck}, nil
}

func procurementAttemptCoreFailure(err error) error {
	switch err {
	case fundinglotverify.ErrProcurementCostApprovalRowsResource:
		return fundingSourceReadResource
	case fundinglotverify.ErrProcurementCostApprovalRowsCanceled:
		return fundingSourceReadCanceled
	case fundinglotverify.ErrProcurementCostApprovalRowsTimeout:
		return fundingSourceReadTimeout
	case fundinglotverify.ErrProcurementBudgetRowsResource:
		return fundingSourceReadResource
	case fundinglotverify.ErrProcurementBudgetRowsCanceled:
		return fundingSourceReadCanceled
	case fundinglotverify.ErrProcurementBudgetRowsTimeout:
		return fundingSourceReadTimeout
	case fundinglotverify.ErrProcurementAttemptBindingRowsResource:
		return fundingSourceReadResource
	case fundinglotverify.ErrProcurementAttemptBindingRowsCanceled:
		return fundingSourceReadCanceled
	case fundinglotverify.ErrProcurementAttemptBindingRowsTimeout:
		return fundingSourceReadTimeout
	default:
		return fundingSourceReadExecution
	}
}
