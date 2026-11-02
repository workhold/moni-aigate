package billing

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func procurementBudgetPageSQL(schema, table, cursorColumn string, columns ...string) string {
	identifier := pgx.Identifier{schema, table}.Sanitize()
	cursor := pgx.Identifier{cursorColumn}.Sanitize()
	return "SELECT " + strings.Join(columns, ",") + " FROM " + identifier +
		" WHERE NOT $1::pg_catalog.bool OR " + cursor + " COLLATE \"C\" > $2::pg_catalog.text COLLATE \"C\"" +
		" ORDER BY " + cursor + " COLLATE \"C\" LIMIT 128"
}

func readProcurementBudgetFacts(ctx context.Context, query fundingSourceQuery, schema string) (fundinglotverify.ProcurementBudgetRows, fundinglotverify.ProcurementBudgetProjectionCheck, error) {
	if ctx == nil {
		return fundinglotverify.ProcurementBudgetRows{}, fundinglotverify.ProcurementBudgetProjectionCheck{}, fundingSourceReadInput
	}
	if ctx.Err() != nil {
		return fundinglotverify.ProcurementBudgetRows{}, fundinglotverify.ProcurementBudgetProjectionCheck{}, fundingSourceReadFailure(ctx)
	}
	if query == nil || !validProcurementBudgetSchema(schema) {
		return fundinglotverify.ProcurementBudgetRows{}, fundinglotverify.ProcurementBudgetProjectionCheck{}, fundingSourceReadInput
	}
	var facts fundinglotverify.ProcurementBudgetRows
	var budget fundingSourceReadBudget

	if err := readFundingSourcePages(ctx, query, procurementBudgetPageSQL(schema, "procurement_accounts", "id",
		"id", "held_amount::pg_catalog.text", "spent_amount::pg_catalog.text"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementBudgetAccountRow
		if err := source.Scan(&row.ID, &row.HeldAmount, &row.SpentAmount); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		fields := []string{row.ID, row.HeldAmount, row.SpentAmount}
		return row.ID, fields, func() { facts.Accounts = append(facts.Accounts, row) }, nil
	}, &budget); err != nil {
		return fundinglotverify.ProcurementBudgetRows{}, fundinglotverify.ProcurementBudgetProjectionCheck{}, err
	}
	if err := readFundingSourcePages(ctx, query, procurementBudgetPageSQL(schema, "procurement_reservations", "attempt_id",
		"attempt_id", "account_id", "upper_amount::pg_catalog.text", "held_amount::pg_catalog.text", "spent_amount::pg_catalog.text", "state", "anomaly"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementBudgetReservationRow
		if err := source.Scan(&row.AttemptID, &row.AccountID, &row.UpperAmount, &row.HeldAmount, &row.SpentAmount, &row.State, &row.Anomaly); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		fields := []string{row.AttemptID, row.AccountID, row.UpperAmount, row.HeldAmount, row.SpentAmount, row.State}
		return row.AttemptID, fields, func() { facts.Reservations = append(facts.Reservations, row) }, nil
	}, &budget); err != nil {
		return fundinglotverify.ProcurementBudgetRows{}, fundinglotverify.ProcurementBudgetProjectionCheck{}, err
	}
	if err := readFundingSourcePages(ctx, query, procurementBudgetPageSQL(schema, "procurement_budget_events", "id",
		"id", "event_key", "account_id", "attempt_id", "held_delta::pg_catalog.text", "spent_delta::pg_catalog.text"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementBudgetEventRow
		if err := source.Scan(&row.ID, &row.EventKey, &row.AccountID, &row.AttemptID, &row.HeldDelta, &row.SpentDelta); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		fields := []string{row.ID, row.EventKey, row.AccountID, row.AttemptID, row.HeldDelta, row.SpentDelta}
		return row.ID, fields, func() { facts.Events = append(facts.Events, row) }, nil
	}, &budget); err != nil {
		return fundinglotverify.ProcurementBudgetRows{}, fundinglotverify.ProcurementBudgetProjectionCheck{}, err
	}
	check, err := fundinglotverify.CheckProcurementBudgetProjection(ctx, facts)
	if err != nil {
		switch err {
		case fundinglotverify.ErrProcurementBudgetRowsResource:
			return fundinglotverify.ProcurementBudgetRows{}, fundinglotverify.ProcurementBudgetProjectionCheck{}, fundingSourceReadResource
		case fundinglotverify.ErrProcurementBudgetRowsCanceled:
			return fundinglotverify.ProcurementBudgetRows{}, fundinglotverify.ProcurementBudgetProjectionCheck{}, fundingSourceReadCanceled
		case fundinglotverify.ErrProcurementBudgetRowsTimeout:
			return fundinglotverify.ProcurementBudgetRows{}, fundinglotverify.ProcurementBudgetProjectionCheck{}, fundingSourceReadTimeout
		default:
			return fundinglotverify.ProcurementBudgetRows{}, fundinglotverify.ProcurementBudgetProjectionCheck{}, fundingSourceReadExecution
		}
	}
	return facts, check, nil
}
