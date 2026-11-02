package billing

import (
	"context"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func readSandboxCaptureReceiptFacts(ctx context.Context, query fundingSourceQuery, schema string) (fundinglotverify.FundingSandboxCaptureRows, fundinglotverify.FundingSandboxCaptureLinkCheck, error) {
	if ctx.Err() != nil {
		return fundinglotverify.FundingSandboxCaptureRows{}, fundinglotverify.FundingSandboxCaptureLinkCheck{}, fundingSourceReadFailure(ctx)
	}
	if query == nil || !validFundingSourceSchema(schema) {
		return fundinglotverify.FundingSandboxCaptureRows{}, fundinglotverify.FundingSandboxCaptureLinkCheck{}, fundingSourceReadInput
	}
	var rows fundinglotverify.FundingSandboxCaptureRows
	var budget fundingSourceReadBudget
	if err := readFundingSourcePages(ctx, query, fundingSourcePageSQL(schema, "funding_receipts",
		"id", "tenant_id", "source_kind", "source_id", "amount::pg_catalog.text", "currency", "journal_id"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.FundingSourceReceiptRow
		if err := source.Scan(&row.ID, &row.TenantID, &row.SourceKind, &row.SourceID, &row.Amount, &row.Currency, &row.JournalID); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		fields := []string{row.ID, row.TenantID, row.SourceKind, row.SourceID, row.Amount, row.Currency, row.JournalID}
		return row.ID, fields, func() { rows.Receipts = append(rows.Receipts, row) }, nil
	}, &budget); err != nil {
		return fundinglotverify.FundingSandboxCaptureRows{}, fundinglotverify.FundingSandboxCaptureLinkCheck{}, err
	}
	if err := readFundingSourcePages(ctx, query, fundingSourcePageSQL(schema, "payment_captures",
		"id", "tenant_id", "amount::pg_catalog.text", "wallet_credit_amount::pg_catalog.text", "currency"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.FundingSandboxCaptureRow
		if err := source.Scan(&row.ID, &row.TenantID, &row.Amount, &row.WalletCreditAmount, &row.Currency); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		fields := []string{row.ID, row.TenantID, row.Amount, row.WalletCreditAmount, row.Currency}
		return row.ID, fields, func() { rows.Captures = append(rows.Captures, row) }, nil
	}, &budget); err != nil {
		return fundinglotverify.FundingSandboxCaptureRows{}, fundinglotverify.FundingSandboxCaptureLinkCheck{}, err
	}
	check, err := fundinglotverify.CheckSandboxCaptureReceiptLinks(ctx, rows)
	if err != nil {
		switch err {
		case fundinglotverify.ErrFundingSourceRowsResource:
			return fundinglotverify.FundingSandboxCaptureRows{}, fundinglotverify.FundingSandboxCaptureLinkCheck{}, fundingSourceReadResource
		case fundinglotverify.ErrFundingSourceRowsCanceled:
			return fundinglotverify.FundingSandboxCaptureRows{}, fundinglotverify.FundingSandboxCaptureLinkCheck{}, fundingSourceReadCanceled
		case fundinglotverify.ErrFundingSourceRowsTimeout:
			return fundinglotverify.FundingSandboxCaptureRows{}, fundinglotverify.FundingSandboxCaptureLinkCheck{}, fundingSourceReadTimeout
		default:
			return fundinglotverify.FundingSandboxCaptureRows{}, fundinglotverify.FundingSandboxCaptureLinkCheck{}, fundingSourceReadExecution
		}
	}
	return rows, check, nil
}
